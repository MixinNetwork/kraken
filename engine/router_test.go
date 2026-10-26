package engine

import (
	"encoding/json"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MixinNetwork/mixin/logger"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/vnet"
	"github.com/pion/webrtc/v4"
)

// Blocking interface enumeration keeps ICE gathering pending without using
// real sockets or depending on the machine's network configuration.
type blockingGatherNet struct {
	transport.Net
	blocked     atomic.Bool
	release     chan struct{}
	releaseOnce sync.Once
}

func (n *blockingGatherNet) Interfaces() ([]*transport.Interface, error) {
	if n.blocked.Load() {
		<-n.release
	}
	return n.Net.Interfaces()
}

func (n *blockingGatherNet) unblock() {
	n.releaseOnce.Do(func() { close(n.release) })
}

func testGatheringConnection(t *testing.T) (*webrtc.PeerConnection, *blockingGatherNet) {
	t.Helper()
	network, err := vnet.NewNet(&vnet.NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	blocked := &blockingGatherNet{Net: network, release: make(chan struct{})}
	settings := webrtc.SettingEngine{}
	settings.SetNet(blocked)
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	pc := testPeerConnection(t, webrtc.WithSettingEngine(settings))
	t.Cleanup(blocked.unblock)
	return pc, blocked
}

func testAudioConnection(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	pc := testPeerConnection(t)
	track, err := webrtc.NewTrackLocalStaticRTP(testOpus(111).RTPCodecCapability, "microphone", "stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.AddTrack(track); err != nil {
		t.Fatal(err)
	}
	return pc
}

func testOffer(t *testing.T, pc *webrtc.PeerConnection, restart bool) *webrtc.SessionDescription {
	t.Helper()
	offer, err := pc.CreateOffer(&webrtc.OfferOptions{ICERestart: restart})
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalDescription(pc, offer, peerTrackReadTimeout); err != nil {
		t.Fatal(err)
	}
	return pc.LocalDescription()
}

func testAnswer(t *testing.T, pc *webrtc.PeerConnection, offer *webrtc.SessionDescription) *webrtc.SessionDescription {
	t.Helper()
	if err := pc.SetRemoteDescription(*offer); err != nil {
		t.Fatal(err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := setLocalDescription(pc, answer, peerTrackReadTimeout); err != nil {
		t.Fatal(err)
	}
	return pc.LocalDescription()
}

func testDescriptionJSON(t *testing.T, desc *webrtc.SessionDescription) string {
	t.Helper()
	b, err := json.Marshal(desc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRestartRejectsCloseDuringGathering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pc, network := testGatheringConnection(t)
		defer network.unblock()
		remote := testAudioConnection(t)
		initial := testAnswer(t, pc, testOffer(t, remote, false))
		if err := remote.SetRemoteDescription(*initial); err != nil {
			t.Fatal(err)
		}
		peer := BuildPeer("room", "user", pc, "", false)
		t.Cleanup(func() { _ = peer.CloseWithTimeout() })
		engine := &Engine{rooms: rmapAllocate()}
		if _, err := engine.addPeer(peer, 0); err != nil {
			t.Fatal(err)
		}
		offer := testDescriptionJSON(t, testOffer(t, remote, true))
		network.blocked.Store(true)
		var desc *webrtc.SessionDescription
		var restartErr, closeErr error
		go func() {
			desc, restartErr = NewRouter(engine).restart(peer.rid, peer.uid, peer.cid, offer)
		}()
		synctest.Wait()
		if pc.SignalingState() != webrtc.SignalingStateStable || pc.ICEGatheringState() != webrtc.ICEGatheringStateGathering || pc.LocalDescription().SDP == initial.SDP {
			t.Fatal("restart did not reach ICE gathering with its new answer")
		}
		go func() { closeErr = peer.CloseWithTimeout() }()
		synctest.Wait()
		if pc.SignalingState() != webrtc.SignalingStateClosed {
			t.Fatal("connection did not start closing")
		}
		network.unblock()
		synctest.Wait()
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if restartErr == nil || desc != nil {
			t.Fatalf("restart succeeded after close: desc=%v, err=%v", desc, restartErr)
		}
	})
}

func TestPublishPreservesInitialAnswerDuringReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		blocked := &blockingCloseInterceptor{release: make(chan struct{})}
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(blocked.release) }) }
		defer release()
		registry := &interceptor.Registry{}
		registry.Add(testInterceptorFactory{impl: blocked})
		old := BuildPeer("room", "user", testPeerConnection(t, webrtc.WithInterceptorRegistry(registry)), "", false)
		t.Cleanup(func() { _ = old.CloseWithTimeout() })
		engine := &Engine{rooms: rmapAllocate()}
		pub := testPeer(t, "room", "speaker")
		if _, err := pub.addTrack(testOpus(111)); err != nil {
			t.Fatal(err)
		}
		for _, peer := range []*Peer{old, pub} {
			if _, err := engine.addPeer(peer, 0); err != nil {
				t.Fatal(err)
			}
		}
		replacement := testPeer(t, "room", "user")
		remote := testAudioConnection(t)
		initial := testAnswer(t, replacement.pc, testOffer(t, remote, false))
		router := NewRouter(engine)
		var cid string
		var desc *webrtc.SessionDescription
		var publishErr error
		go func() { cid, desc, publishErr = router.publishPeer(replacement, 0) }()
		synctest.Wait()
		if blocked.calls.Load() != 1 {
			t.Fatal("publish did not reach the old peer's shutdown")
		}
		peers, err := router.list("room")
		if err != nil {
			t.Fatal(err)
		}
		var replacementID string
		for _, peer := range peers {
			if peer["id"] == replacement.uid {
				replacementID = peer["track"].(string)
			}
		}
		if replacementID != replacement.cid {
			t.Fatal("replacement was not visible during shutdown")
		}
		offer, err := router.subscribe("room", "user", replacementID)
		if err != nil || offer == nil || offer.Type != webrtc.SDPTypeOffer {
			t.Fatalf("subscription offer: %v, %v", offer, err)
		}
		release()
		synctest.Wait()
		if publishErr != nil || cid != replacement.cid || desc == nil || desc.Type != webrtc.SDPTypeAnswer || desc.SDP != initial.SDP {
			t.Fatalf("publish did not preserve its answer: cid=%s, desc=%v, err=%v", cid, desc, publishErr)
		}
		if err := remote.SetRemoteDescription(*desc); err != nil {
			t.Fatalf("publish response cannot answer the original offer: %v", err)
		}
	})
}

// The existing log call is between doSubscribe and returning its response.
// Pause there to allow later signaling to finish before the first RPC returns.
type subscribeLogGate struct {
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
	paused      atomic.Bool
}

func (g *subscribeLogGate) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "peer.doSubscribe(") {
		g.once.Do(func() {
			g.paused.Store(true)
			<-g.release
		})
	}
	return len(b), nil
}

func (g *subscribeLogGate) unblock() {
	g.releaseOnce.Do(func() { close(g.release) })
}

func TestSubscribePreservesOfferAcrossRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine := &Engine{rooms: rmapAllocate()}
		sub, pub := testPeer(t, "room", "sub"), testPeer(t, "room", "pub")
		if _, err := pub.addTrack(testOpus(111)); err != nil {
			t.Fatal(err)
		}
		for _, peer := range []*Peer{sub, pub} {
			if _, err := engine.addPeer(peer, 0); err != nil {
				t.Fatal(err)
			}
		}
		router := NewRouter(engine)
		gate := &subscribeLogGate{release: make(chan struct{})}
		writer := log.Writer()
		log.SetOutput(gate)
		logger.SetLevel(logger.INFO)
		defer func() {
			gate.unblock()
			logger.SetLevel(0)
			log.SetOutput(writer)
		}()
		var desc *webrtc.SessionDescription
		var subscribeErr error
		go func() { desc, subscribeErr = router.subscribe("room", "sub", sub.cid) }()
		synctest.Wait()
		if !gate.paused.Load() {
			t.Fatal("subscribe did not reach the response boundary")
		}
		// The paused writer holds the standard logger's output lock. Disable
		// further logging so later RPCs can complete while this one is paused.
		logger.SetLevel(0)
		offer, err := router.subscribe("room", "sub", sub.cid)
		if err != nil || offer == nil || offer.Type != webrtc.SDPTypeOffer {
			t.Fatalf("repeated subscribe: %v, %v", offer, err)
		}
		remote := testPeerConnection(t)
		answer := testAnswer(t, remote, offer)
		if err := router.answer("room", "sub", sub.cid, testDescriptionJSON(t, answer)); err != nil {
			t.Fatal(err)
		}
		restartAnswer, err := router.restart("room", "sub", sub.cid, testDescriptionJSON(t, testOffer(t, remote, true)))
		if err != nil || restartAnswer == nil || restartAnswer.Type != webrtc.SDPTypeAnswer {
			t.Fatalf("restart: %v, %v", restartAnswer, err)
		}
		gate.unblock()
		synctest.Wait()
		if subscribeErr != nil || desc == nil || desc.Type != webrtc.SDPTypeOffer || desc.SDP != offer.SDP {
			t.Fatalf("subscribe returned a later description: %v, %v", desc, subscribeErr)
		}
	})
}

func TestSignalingPreservesGatheringTimeout(t *testing.T) {
	for _, operation := range []string{"subscribe", "restart"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				pc, network := testGatheringConnection(t)
				defer network.unblock()
				peer := BuildPeer("room", "user", pc, "", false)
				t.Cleanup(func() { _ = peer.CloseWithTimeout() })
				// Simulate an established publisher so its initial media timer
				// cannot close it at the same deadline as an ICE restart.
				close(peer.connected)
				engine := &Engine{rooms: rmapAllocate()}
				if _, err := engine.addPeer(peer, 0); err != nil {
					t.Fatal(err)
				}
				router := NewRouter(engine)
				var run func() (*webrtc.SessionDescription, error)
				var timeout time.Duration
				if operation == "subscribe" {
					pub := testPeer(t, "room", "pub")
					if _, err := pub.addTrack(testOpus(111)); err != nil {
						t.Fatal(err)
					}
					if _, err := engine.addPeer(pub, 0); err != nil {
						t.Fatal(err)
					}
					// Initialize the ICE agent before blocking enumeration during
					// gathering, rather than while CreateOffer creates the agent.
					if _, err := pc.CreateOffer(nil); err != nil {
						t.Fatal(err)
					}
					run = func() (*webrtc.SessionDescription, error) {
						return router.subscribe("room", "user", peer.cid)
					}
					timeout = peerTrackReadTimeout
				} else {
					remote := testAudioConnection(t)
					initial := testAnswer(t, pc, testOffer(t, remote, false))
					if err := remote.SetRemoteDescription(*initial); err != nil {
						t.Fatal(err)
					}
					offer := testDescriptionJSON(t, testOffer(t, remote, true))
					run = func() (*webrtc.SessionDescription, error) {
						return router.restart("room", "user", peer.cid, offer)
					}
					timeout = peerTrackConnectionTimeout
				}
				network.blocked.Store(true)
				var desc *webrtc.SessionDescription
				var err error
				go func() { desc, err = run() }()
				synctest.Wait()
				if pc.ICEGatheringState() != webrtc.ICEGatheringStateGathering {
					t.Fatal("signaling did not reach ICE gathering")
				}
				time.Sleep(timeout)
				synctest.Wait()
				if !peer.isClosed() {
					t.Fatal("timed-out signaling did not start closing the peer")
				}
				network.unblock()
				synctest.Wait()
				assertErrorCode(t, err, ErrorServerTimeout)
				if desc != nil {
					t.Fatalf("timed-out signaling returned a description: %v", desc)
				}
				if got := errorResponse(err).Description; !strings.HasPrefix(got, "timeout after ") {
					t.Fatalf("timeout description was wrapped: %s", got)
				}
			})
		})
	}
}
