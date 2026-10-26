package engine

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/MixinNetwork/mixin/logger"
	"github.com/pion/interceptor"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

type Router struct {
	engine *Engine
}

func NewRouter(engine *Engine) *Router {
	return &Router{engine: engine}
}

func (r *Router) info() any {
	r.engine.stateMu.RLock()
	defer r.engine.stateMu.RUnlock()

	return r.engine.state
}

func (r *Router) list(rid string) ([]map[string]any, error) {
	list := make([]map[string]any, 0)
	room := r.engine.getRoom(rid)
	if room == nil {
		return list, nil
	}
	peers := room.PeersCopy()
	for _, p := range peers {
		p.RLock()
		if !p.closed {
			list = append(list, map[string]any{
				"id":    p.uid,
				"track": p.cid,
				"mute":  p.listenOnly,
			})
		}
		p.RUnlock()
	}
	return list, nil
}

func (r *Router) mute(rid, uid string) map[string]any {
	room := r.engine.getRoom(rid)
	if room == nil {
		return nil
	}
	room.RLock()
	p := room.m[uid]
	room.RUnlock()
	if p == nil {
		return nil
	}
	p.Lock()
	defer p.Unlock()
	if p.closed {
		return nil
	}
	p.listenOnly = !p.listenOnly
	return map[string]any{
		"id":    p.uid,
		"track": p.cid,
		"mute":  p.listenOnly,
	}
}

func (r *Router) create(rid, uid, callback string, listenOnly bool, offer webrtc.SessionDescription) (*Peer, error) {
	se := webrtc.SettingEngine{}
	se.SetLite(true)
	se.EnableSCTPZeroChecksum(true)
	se.SetInterfaceFilter(func(in string) bool { return in == r.engine.Interface })
	err := se.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
		External:        []string{r.engine.IP},
		AsCandidateType: webrtc.ICECandidateTypeHost,
		Mode:            webrtc.ICEAddressRewriteReplace,
	})
	if err != nil {
		return nil, err
	}
	se.SetICETimeouts(10*time.Second, 20*time.Second, 2*time.Second)
	se.SetDTLSInsecureSkipHelloVerify(true)
	se.SetReceiveMTU(8192)
	err = se.SetEphemeralUDPPortRange(r.engine.PortMin, r.engine.PortMax)
	if err != nil {
		return nil, err
	}

	me := &webrtc.MediaEngine{}
	opusChrome := webrtc.RTPCodecParameters{
		MimeType:     webrtc.MimeTypeOpus,
		ClockRate:    48000,
		Channels:     2,
		SDPFmtpLine:  "minptime=10;useinbandfec=1",
		RTCPFeedback: nil,
		PayloadType:  111,
	}
	opusFirefox := webrtc.RTPCodecParameters{
		MimeType:     webrtc.MimeTypeOpus,
		ClockRate:    48000,
		Channels:     2,
		SDPFmtpLine:  "minptime=10;useinbandfec=1",
		RTCPFeedback: nil,
		PayloadType:  109,
	}
	err = me.RegisterCodec(opusChrome, webrtc.RTPCodecTypeAudio)
	if err != nil {
		return nil, err
	}
	err = me.RegisterCodec(opusFirefox, webrtc.RTPCodecTypeAudio)
	if err != nil {
		return nil, err
	}

	ir := &interceptor.Registry{}
	err = webrtc.RegisterDefaultInterceptors(me, ir)
	if err != nil {
		panic(err)
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(se), webrtc.WithInterceptorRegistry(ir))

	pcConfig := webrtc.Configuration{
		BundlePolicy:  webrtc.BundlePolicyMaxBundle,
		RTCPMuxPolicy: webrtc.RTCPMuxPolicyRequire,
	}
	pc, err := api.NewPeerConnection(pcConfig)
	if err != nil {
		return nil, buildError(ErrorServerNewPeerConnection, err)
	}

	err = pc.SetRemoteDescription(offer)
	if err != nil {
		pc.Close()
		return nil, buildError(ErrorServerSetRemoteOffer, err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return nil, buildError(ErrorServerCreateAnswer, err)
	}
	err = setLocalDescription(pc, answer, peerTrackConnectionTimeout)
	if err != nil {
		pc.Close()
		return nil, buildError(ErrorServerSetLocalAnswer, err)
	}

	peer := BuildPeer(rid, uid, pc, callback, listenOnly)
	return peer, nil
}

func (r *Router) publish(rid, uid string, jsep string, limit int, callback string, listenOnly bool) (string, *webrtc.SessionDescription, error) {
	if err := validateId(rid); err != nil {
		return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid rid format %s %s", rid, err.Error()))
	}
	if err := validateId(uid); err != nil {
		return "", nil, buildError(ErrorInvalidParams, fmt.Errorf("invalid uid format %s %s", uid, err.Error()))
	}
	var offer webrtc.SessionDescription
	err := json.Unmarshal([]byte(jsep), &offer)
	if err != nil {
		return "", nil, buildError(ErrorInvalidSDP, err)
	}
	if offer.Type != webrtc.SDPTypeOffer {
		return "", nil, buildError(ErrorInvalidSDP, fmt.Errorf("invalid sdp type %s", offer.Type))
	}

	parser := sdp.SessionDescription{}
	err = parser.UnmarshalString(offer.SDP)
	if err != nil {
		return "", nil, buildError(ErrorInvalidSDP, err)
	}

	// Reject an already full room before creating a connection, then check
	// again when committing because other publications may finish meanwhile.
	if room := r.engine.getRoom(rid); room != nil {
		room.RLock()
		err = room.checkLimit(uid, limit)
		room.RUnlock()
		if err != nil {
			return "", nil, err
		}
	}
	peer, err := r.create(rid, uid, callback, listenOnly, offer)
	if err != nil {
		return "", nil, err
	}
	old, err := r.engine.addPeer(peer, limit)
	if err != nil {
		_ = peer.CloseWithTimeout()
		return "", nil, err
	}
	if old != nil {
		_ = old.CloseWithTimeout()
	}
	return peer.cid, peer.pc.LocalDescription(), nil
}

func (r *Router) restart(rid, uid, cid string, jsep string) (*webrtc.SessionDescription, error) {
	peer, err := r.engine.getPeer(rid, uid, cid)
	if err != nil {
		return nil, err
	}

	var offer webrtc.SessionDescription
	err = json.Unmarshal([]byte(jsep), &offer)
	if err != nil {
		return nil, buildError(ErrorInvalidSDP, err)
	}
	if offer.Type != webrtc.SDPTypeOffer {
		return nil, buildError(ErrorInvalidSDP, fmt.Errorf("invalid sdp type %s", offer.Type))
	}
	parser := sdp.SessionDescription{}
	err = parser.UnmarshalString(offer.SDP)
	if err != nil {
		return nil, buildError(ErrorInvalidSDP, err)
	}

	peer.signalingMu.Lock()
	defer peer.signalingMu.Unlock()

	err = func() error {
		err := peer.pc.SetRemoteDescription(offer)
		if err != nil {
			return buildError(ErrorServerSetRemoteOffer, err)
		}
		answer, err := peer.pc.CreateAnswer(nil)
		if err != nil {
			return buildError(ErrorServerCreateAnswer, err)
		}
		err = setLocalDescription(peer.pc, answer, peerTrackConnectionTimeout)
		if err != nil {
			return buildError(ErrorServerSetLocalAnswer, err)
		}
		return nil
	}()

	if err != nil {
		_ = peer.CloseWithTimeout()
		return nil, err
	}
	return peer.pc.LocalDescription(), nil
}

func (r *Router) end(rid, uid, cid string) error {
	peer, err := r.engine.getPeer(rid, uid, cid)
	if err != nil {
		return err
	}

	return peer.CloseWithTimeout()
}

func (r *Router) trickle(rid, uid, cid string, candi string) error {
	var ici webrtc.ICECandidateInit
	err := json.Unmarshal([]byte(candi), &ici)
	if err != nil {
		return buildError(ErrorInvalidCandidate, err)
	}
	if ici.Candidate == "" {
		return nil
	}

	peer, err := r.engine.getPeer(rid, uid, cid)
	if err != nil {
		return err
	}
	peer.signalingMu.Lock()
	defer peer.signalingMu.Unlock()

	return peer.pc.AddICECandidate(ici)
}

func (r *Router) subscribe(rid, uid, cid string) (*webrtc.SessionDescription, error) {
	room := r.engine.getRoom(rid)
	if room == nil {
		return nil, buildError(ErrorPeerNotFound, fmt.Errorf("peer %s not found in %s", uid, rid))
	}
	peer, err := room.GetPeer(uid, cid)
	if err != nil {
		return nil, err
	}

	err = peer.doSubscribe(room)
	logger.Printf("peer.doSubscribe(%s, %s, %s) => %v", rid, uid, cid, err)
	if err != nil {
		_ = peer.CloseWithTimeout()
		return nil, err
	}
	return peer.pc.LocalDescription(), nil
}

func (peer *Peer) doSubscribe(room *pmap) error {
	peer.signalingMu.Lock()
	defer peer.signalingMu.Unlock()
	if peer.isClosed() {
		return buildError(ErrorPeerClosed, fmt.Errorf("peer %s closed in %s", peer.uid, peer.rid))
	}
	// Finish the outstanding offer before applying subscription changes. A
	// repeated subscribe returns that offer so the client can still answer it.
	if peer.pc.SignalingState() == webrtc.SignalingStateHaveLocalOffer {
		return nil
	}
	peers := room.PeersCopy()

	var renegotiate bool
	for uid, old := range peer.publishers {
		if peers[uid] != nil {
			continue
		}
		if err := peer.pc.RemoveTrack(old.rtp); err != nil {
			return fmt.Errorf("pc.RemoveTrack(%s, %s) => %w", uid, peer.id(), err)
		}
		delete(peer.publishers, uid)
		renegotiate = true
	}
	for _, pub := range peers {
		if pub.uid == peer.uid {
			continue
		}
		res, err := peer.connectPublisher(pub)
		if err != nil {
			return err
		}
		renegotiate = renegotiate || res
	}
	if renegotiate {
		offer, err := peer.pc.CreateOffer(nil)
		if err != nil {
			return buildError(ErrorServerCreateOffer, err)
		}
		if err := setLocalDescription(peer.pc, offer, peerTrackReadTimeout); err != nil {
			return buildError(ErrorServerSetLocalOffer, err)
		}
	}
	return nil
}

// connectPublisher requires sub.signalingMu to be held.
func (sub *Peer) connectPublisher(pub *Peer) (bool, error) {
	pub.RLock()
	track := pub.track
	pub.RUnlock()

	var renegotiate bool
	old := sub.publishers[pub.uid]
	if old != nil && (track == nil || old.id != pub.cid) {
		if err := sub.pc.RemoveTrack(old.rtp); err != nil {
			return false, fmt.Errorf("pc.RemoveTrack(%s, %s) => %w", pub.id(), sub.id(), err)
		}
		delete(sub.publishers, pub.uid)
		renegotiate = true
	}
	if track != nil && (old == nil || old.id != pub.cid) {
		sender, err := sub.pc.AddTrack(track)
		logger.Printf("pc.AddTrack(%s, %s) => %v %v", sub.id(), pub.id(), sender, err)
		if err != nil {
			return false, fmt.Errorf("pc.AddTrack(%s, %s) => %w", sub.id(), pub.id(), err)
		}
		if id := sender.Track().ID(); id != pub.cid {
			return false, fmt.Errorf("malformed peer and track id %s %s", pub.cid, id)
		}
		sub.publishers[pub.uid] = &Sender{id: pub.cid, rtp: sender}
		renegotiate = true
	}
	return renegotiate, nil
}

func (r *Router) answer(rid, uid, cid string, jsep string) error {
	var answer webrtc.SessionDescription
	err := json.Unmarshal([]byte(jsep), &answer)
	if err != nil {
		return buildError(ErrorInvalidSDP, err)
	}
	if answer.Type != webrtc.SDPTypeAnswer {
		return buildError(ErrorInvalidSDP, fmt.Errorf("invalid sdp type %s", answer.Type))
	}

	parser := sdp.SessionDescription{}
	err = parser.UnmarshalString(answer.SDP)
	if err != nil {
		return buildError(ErrorInvalidSDP, err)
	}

	peer, err := r.engine.getPeer(rid, uid, cid)
	if err != nil {
		return err
	}

	peer.signalingMu.Lock()
	defer peer.signalingMu.Unlock()

	if peer.pc.SignalingState() == webrtc.SignalingStateStable {
		return nil
	}
	err = peer.pc.SetRemoteDescription(answer)
	logger.Printf("pc.SetRemoteDescription(%s, %s, %s) => %v", rid, uid, cid, err)
	if err != nil {
		return buildError(ErrorServerSetRemoteAnswer, err)
	}
	return nil
}

func validateId(id string) error {
	if len(id) > 256 {
		return fmt.Errorf("id %s too long, the maximum is %d", id, 256)
	}
	uid, err := url.QueryUnescape(id)
	if err != nil {
		return err
	}
	if eid := url.QueryEscape(uid); eid != id {
		return fmt.Errorf("unmatch %s %s", id, eid)
	}
	return nil
}
