package engine

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/MixinNetwork/mixin/logger"
	"github.com/gofrs/uuid/v5"
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
	r.engine.rooms.RLock()
	defer r.engine.rooms.RUnlock()

	return r.engine.state
}

func (r *Router) list(rid string) ([]map[string]any, error) {
	room := r.engine.GetRoom(rid)
	peers := room.PeersCopy()
	list := make([]map[string]any, 0)
	for _, p := range peers {
		cid := uuid.FromStringOrNil(p.cid)
		if cid.String() == uuid.Nil.String() {
			continue
		}
		list = append(list, map[string]any{
			"id":    p.uid,
			"track": cid.String(),
			"mute":  p.listenOnly,
		})
	}
	return list, nil
}

func (r *Router) mute(rid, uid string) map[string]any {
	room := r.engine.GetRoom(rid)
	peers := room.PeersCopy()
	for _, p := range peers {
		if p.uid != uid {
			continue
		}
		cid := uuid.FromStringOrNil(p.cid)
		if cid.String() == uuid.Nil.String() {
			continue
		}
		p.listenOnly = !p.listenOnly
		return map[string]any{
			"id":    p.uid,
			"track": cid.String(),
			"mute":  p.listenOnly,
		}
	}
	return nil
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
	err = setLocalDescription(pc, answer)
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

	room := r.engine.GetRoom(rid)
	if limit > 0 {
		peers := room.PeersCopy()
		for i, p := range peers {
			cid := uuid.FromStringOrNil(p.cid)
			if cid.String() == uuid.Nil.String() || uid == i {
				continue
			}
			limit--
		}
		if limit <= 0 {
			return "", nil, buildError(ErrorRoomFull, fmt.Errorf("room full %d", limit))
		}
	}

	room.Lock()
	defer room.Unlock()

	var peer *Peer
	err = lockRunWithTimeout(func() error {
		pub, err := r.create(rid, uid, callback, listenOnly, offer)
		peer = pub
		return err
	}, peerTrackConnectionTimeout)
	if err != nil {
		return "", nil, err
	}

	old := room.m[peer.uid]
	if old != nil {
		_ = old.CloseWithTimeout()
	}
	room.m[peer.uid] = peer
	return peer.cid, peer.pc.LocalDescription(), nil
}

func (r *Router) restart(rid, uid, cid string, jsep string) (*webrtc.SessionDescription, error) {
	room := r.engine.GetRoom(rid)
	peer, err := room.GetPeer(uid, cid)
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

	peer.Lock()
	defer peer.Unlock()

	err = lockRunWithTimeout(func() error {
		err := peer.pc.SetRemoteDescription(offer)
		if err != nil {
			return buildError(ErrorServerSetRemoteOffer, err)
		}
		answer, err := peer.pc.CreateAnswer(nil)
		if err != nil {
			return buildError(ErrorServerCreateAnswer, err)
		}
		err = setLocalDescription(peer.pc, answer)
		if err != nil {
			return buildError(ErrorServerSetLocalAnswer, err)
		}
		return nil
	}, peerTrackConnectionTimeout)

	if err != nil {
		_ = lockRunWithTimeout(func() error {
			return peer.close()
		}, peerTrackReadTimeout)
		return nil, err
	}
	return peer.pc.LocalDescription(), nil
}

func (r *Router) end(rid, uid, cid string) error {
	room := r.engine.GetRoom(rid)
	peer, err := room.GetPeer(uid, cid)
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

	room := r.engine.GetRoom(rid)
	peer, err := room.GetPeer(uid, cid)
	if err != nil {
		return err
	}
	peer.Lock()
	defer peer.Unlock()

	return lockRunWithTimeout(func() error {
		return peer.pc.AddICECandidate(ici)
	}, peerTrackReadTimeout)
}

func (r *Router) subscribe(rid, uid, cid string) (*webrtc.SessionDescription, error) {
	room := r.engine.GetRoom(rid)
	room.Lock()
	defer room.Unlock()

	peer, err := room.getPeer(uid, cid)
	if err != nil {
		return nil, err
	}

	err = lockRunWithTimeout(func() error {
		err := peer.doSubscribe(room.m)
		logger.Printf("peer.doSubscribe(%s, %s, %s) => %v", rid, uid, cid, err)
		if err != nil {
			_ = peer.close()
			return err
		}
		return nil
	}, peerTrackConnectionTimeout)

	if err != nil {
		return nil, err
	}
	return peer.pc.LocalDescription(), nil
}

func (peer *Peer) doSubscribe(peers map[string]*Peer) error {
	peer.Lock()
	defer peer.Unlock()

	return lockRunWithTimeout(func() error {
		var renegotiate bool
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
			err = setLocalDescription(peer.pc, offer)
			if err != nil {
				return buildError(ErrorServerSetLocalOffer, err)
			}
		}
		return nil
	}, peerTrackReadTimeout)
}

func (sub *Peer) connectPublisher(pub *Peer) (bool, error) {
	pub.RLock()
	defer pub.RUnlock()

	var renegotiate bool
	err := lockRunWithTimeout(func() error {
		old := sub.publishers[pub.uid]
		if old != nil && (pub.track == nil || old.id != pub.cid) {
			err := sub.pc.RemoveTrack(old.rtp)
			if err != nil {
				return fmt.Errorf("pc.RemoveTrack(%s, %s) => %v", pub.id(), sub.id(), err)
			}
			delete(sub.publishers, pub.uid)
			renegotiate = true
		}
		if pub.track != nil && (old == nil || old.id != pub.cid) {
			sender, err := sub.pc.AddTrack(pub.track)
			logger.Printf("pc.AddTrack(%s, %s) => %v %v", sub.id(), pub.id(), sender, err)
			if err != nil {
				return fmt.Errorf("pc.AddTrack(%s, %s) => %v", sub.id(), pub.id(), err)
			}
			if id := sender.Track().ID(); id != pub.cid {
				return fmt.Errorf("malformed peer and track id %s %s", pub.cid, id)
			}
			sub.publishers[pub.uid] = &Sender{id: pub.cid, rtp: sender}
			renegotiate = true
		}
		return nil
	}, peerTrackReadTimeout)
	return renegotiate, err
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

	room := r.engine.GetRoom(rid)
	peer, err := room.GetPeer(uid, cid)
	if err != nil {
		return err
	}

	peer.Lock()
	defer peer.Unlock()

	return lockRunWithTimeout(func() error {
		if peer.pc.SignalingState() == webrtc.SignalingStateStable {
			return nil
		}
		err := peer.pc.SetRemoteDescription(answer)
		logger.Printf("pc.SetRemoteDescription(%s, %s, %s) => %v", rid, uid, cid, err)
		if err != nil {
			return buildError(ErrorServerSetRemoteAnswer, err)
		}
		return nil
	}, peerTrackReadTimeout)
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
