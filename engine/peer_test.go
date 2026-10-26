package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/transport/v5/vnet"
	"github.com/pion/webrtc/v4"
)

func testPeerConnection(t *testing.T, options ...func(*webrtc.API)) *webrtc.PeerConnection {
	t.Helper()
	network, err := vnet.NewNet(&vnet.NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	settings := webrtc.SettingEngine{}
	settings.SetNet(network)
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	options = append([]func(*webrtc.API){webrtc.WithSettingEngine(settings)}, options...)
	pc, err := webrtc.NewAPI(options...).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pc.Close(); err != nil {
			t.Error(err)
		}
	})
	return pc
}

func testPeer(t *testing.T, rid, uid string) *Peer {
	t.Helper()
	pc := testPeerConnection(t)
	peer := BuildPeer(rid, uid, pc, "", false)
	t.Cleanup(func() {
		if err := peer.CloseWithTimeout(); err != nil {
			t.Error(err)
		}
	})
	return peer
}

func testOpus(payloadType webrtc.PayloadType) webrtc.RTPCodecParameters {
	return webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: payloadType,
	}
}

func TestAddTrackUsesNegotiatedCodec(t *testing.T) {
	for _, pt := range []webrtc.PayloadType{96, 109, 111, 112} {
		t.Run(fmt.Sprint(pt), func(t *testing.T) {
			peer := &Peer{cid: "track", uid: "user"}
			added, err := peer.addTrack(testOpus(pt))
			if err != nil || !added || peer.track == nil {
				t.Fatalf("negotiated Opus payload %d was rejected: added=%v, err=%v", pt, added, err)
			}
			if added, err := peer.addTrack(testOpus(pt)); err != nil || added {
				t.Fatalf("duplicate track accepted: added=%v, err=%v", added, err)
			}
		})
	}
	peer := &Peer{cid: "track", uid: "user"}
	codec := testOpus(111)
	codec.MimeType = webrtc.MimeTypeVP8
	if added, err := peer.addTrack(codec); err != nil || added {
		t.Fatalf("accepted non-Opus codec using payload 111: added=%v, err=%v", added, err)
	}
	peer.closed = true
	if added, err := peer.addTrack(testOpus(111)); err != nil || added {
		t.Fatalf("accepted track after close: added=%v, err=%v", added, err)
	}
}

type testRTPWriter struct {
	err    error
	writes int
}

func (w *testRTPWriter) WriteRTP(_ *rtp.Header, payload []byte) (int, error) {
	w.writes++
	return len(payload), w.err
}
func (w *testRTPWriter) Write(payload []byte) (int, error) { return len(payload), w.err }

type testTrackContext struct {
	webrtc.TrackLocalContext
	id     string
	writer *testRTPWriter
}

func (c testTrackContext) CodecParameters() []webrtc.RTPCodecParameters {
	return []webrtc.RTPCodecParameters{testOpus(111)}
}
func (c testTrackContext) SSRC() webrtc.SSRC                       { return 1 }
func (c testTrackContext) SSRCRetransmission() webrtc.SSRC         { return 0 }
func (c testTrackContext) SSRCForwardErrorCorrection() webrtc.SSRC { return 0 }
func (c testTrackContext) WriteStream() webrtc.TrackLocalWriter    { return c.writer }
func (c testTrackContext) ID() string                              { return c.id }

func TestSubscriberFailureDoesNotStopPublisher(t *testing.T) {
	track, err := webrtc.NewTrackLocalStaticRTP(testOpus(111).RTPCodecCapability, "track", "stream")
	if err != nil {
		t.Fatal(err)
	}
	good := &testRTPWriter{}
	bad := &testRTPWriter{err: errors.New("subscriber transport failed")}
	for _, ctx := range []testTrackContext{{id: "bad", writer: bad}, {id: "good", writer: good}} {
		if _, err := track.Bind(ctx); err != nil {
			t.Fatal(err)
		}
	}
	peer := &Peer{uid: "publisher", track: track, queue: make(chan *rtp.Packet, 1)}
	for range 2 {
		peer.queue <- &rtp.Packet{Payload: []byte{0xf8, 0xff, 0xfe}}
		if err := peer.consumeQueue(); err != nil {
			t.Fatalf("subscriber failure stopped forwarding: %v", err)
		}
	}
	if good.writes != 2 {
		t.Fatalf("healthy subscriber received %d packets, want 2", good.writes)
	}
}

func TestCloseWhileForwarding(t *testing.T) {
	peer := testPeer(t, "room", "user")
	if _, err := peer.addTrack(testOpus(111)); err != nil {
		t.Fatal(err)
	}
	id := peer.id()
	for range cap(peer.queue) {
		peer.queue <- &rtp.Packet{}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range cap(peer.queue) {
			_ = peer.consumeQueue()
			_ = peer.id()
		}
	}()
	go func() {
		defer wg.Done()
		if err := peer.CloseWithTimeout(); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if !peer.isClosed() || peer.id() != id {
		t.Fatal("close must mark the peer closed without changing its identity")
	}
	if err := peer.CloseWithTimeout(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

type blockingCloseInterceptor struct {
	interceptor.NoOp
	release chan struct{}
	calls   atomic.Int32
}

func (i *blockingCloseInterceptor) Close() error {
	i.calls.Add(1)
	<-i.release
	return nil
}

type testInterceptorFactory struct{ impl interceptor.Interceptor }

func (f testInterceptorFactory) NewInterceptor(string) (interceptor.Interceptor, error) {
	return f.impl, nil
}

func TestCloseTimeoutReusesAndReleasesWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		blocked := &blockingCloseInterceptor{release: make(chan struct{})}
		defer func() {
			select {
			case <-blocked.release:
			default:
				close(blocked.release)
			}
		}()
		registry := &interceptor.Registry{}
		registry.Add(testInterceptorFactory{impl: blocked})
		pc := testPeerConnection(t, webrtc.WithInterceptorRegistry(registry))
		peer := BuildPeer("room", "user", pc, "", false)
		assertErrorCode(t, peer.CloseWithTimeout(), ErrorServerTimeout)
		assertErrorCode(t, peer.CloseWithTimeout(), ErrorServerTimeout)
		if !peer.isClosed() || blocked.calls.Load() != 1 {
			t.Fatal("timed-out close did not retire the peer or started another worker")
		}
		close(blocked.release)
		synctest.Wait()
		if err := peer.CloseWithTimeout(); err != nil {
			t.Fatalf("completed close: %v", err)
		}
		// The test bubble cannot finish if the closing worker leaks.
	})
}

func TestSubscribeRemovesRetiredPublisher(t *testing.T) {
	engine := &Engine{rooms: rmapAllocate()}
	sub := testPeer(t, "room", "sub")
	pub := testPeer(t, "room", "pub")
	if _, err := pub.addTrack(testOpus(111)); err != nil {
		t.Fatal(err)
	}
	for _, peer := range []*Peer{sub, pub} {
		if _, err := engine.addPeer(peer, 0); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouter(engine)
	offer, err := router.subscribe("room", sub.uid, sub.cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.publishers) != 1 {
		t.Fatal("publisher was not subscribed")
	}
	if err := pub.CloseWithTimeout(); err != nil {
		t.Fatal(err)
	}
	engine.updateState("test", time.Now())
	pending, err := router.subscribe("room", sub.uid, sub.cid)
	if err != nil || sub.isClosed() {
		t.Fatalf("cleanup interrupted a pending negotiation: %v", err)
	}
	if pending.SDP != offer.SDP || len(sub.publishers) != 1 {
		t.Fatal("subscription changed before the outstanding offer was answered")
	}
	// Complete the first negotiation before applying the publisher removal.
	remote := testPeerConnection(t)
	if err := remote.SetRemoteDescription(*offer); err != nil {
		t.Fatal(err)
	}
	answer, err := remote.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalDescription(remote, answer, peerTrackReadTimeout); err != nil {
		t.Fatal(err)
	}
	answerJSON, err := json.Marshal(remote.LocalDescription())
	if err != nil {
		t.Fatal(err)
	}
	if err := router.answer("room", sub.uid, sub.cid, string(answerJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := router.subscribe("room", sub.uid, sub.cid); err != nil {
		t.Fatal(err)
	}
	if len(sub.publishers) != 0 {
		t.Fatal("retired publisher's sender was retained")
	}
}

func TestConcurrentSubscriptions(t *testing.T) {
	engine := &Engine{rooms: rmapAllocate()}
	peers := []*Peer{testPeer(t, "room", "first"), testPeer(t, "room", "second")}
	for _, peer := range peers {
		if _, err := peer.addTrack(testOpus(111)); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.addPeer(peer, 0); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, len(peers))
	for _, peer := range peers {
		go func() {
			<-start
			_, err := NewRouter(engine).subscribe("room", peer.uid, peer.cid)
			results <- err
		}()
	}
	close(start)
	for range peers {
		select {
		case err := <-results:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * peerTrackReadTimeout):
			t.Fatal("concurrent subscriptions deadlocked")
		}
	}
}
