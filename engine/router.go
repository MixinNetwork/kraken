package engine

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"

	"github.com/MixinNetwork/mixin/logger"
	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

type Router struct {
	engine *Engine
}

func NewRouter(engine *Engine) *Router {
	return &Router{engine: engine}
}

func (r *Router) publish(rid, uid string, ss string) (*webrtc.SessionDescription, error) {
	if err := validateId(rid); err != nil {
		return nil, fmt.Errorf("invalid rid format %s %s", rid, err.Error())
	}
	if err := validateId(uid); err != nil {
		return nil, fmt.Errorf("invalid uid format %s %s", uid, err.Error())
	}
	var offer webrtc.SessionDescription
	err := json.Unmarshal([]byte(ss), &offer)
	if err != nil {
		return nil, err
	}
	if offer.Type != webrtc.SDPTypeOffer {
		return nil, fmt.Errorf("invalid sdp type %s", offer.Type)
	}

	parser := sdp.SessionDescription{}
	err = parser.Unmarshal([]byte(offer.SDP))
	if err != nil {
		return nil, err
	}

	se := webrtc.SettingEngine{}
	se.SetLite(true)
	se.SetTrickle(false)
	se.SetInterfaceFilter(func(in string) bool { return in == r.engine.Interface })
	se.SetNAT1To1IPs([]string{r.engine.IP}, webrtc.ICECandidateTypeHost)

	codec := webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000)
	me := webrtc.MediaEngine{}
	me.RegisterCodec(codec)

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(se))

	pcConfig := webrtc.Configuration{
		BundlePolicy:  webrtc.BundlePolicyMaxBundle,
		RTCPMuxPolicy: webrtc.RTCPMuxPolicyRequire,
	}
	pc, err := api.NewPeerConnection(pcConfig)
	if err != nil {
		return nil, err
	}

	peer := r.engine.BuildPeer(rid, uid, pc)
	track, err := pc.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), peer.cid, peer.uid)
	if err != nil {
		return nil, err
	}
	_, err = pc.AddTransceiverFromTrack(track, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendrecv,
	})
	if err != nil {
		return nil, err
	}

	err = pc.SetRemoteDescription(offer)
	if err != nil {
		pc.Close()
		return nil, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return nil, err
	}

	err = pc.SetLocalDescription(answer)
	if err != nil {
		pc.Close()
		return nil, err
	}
	old := r.engine.rooms.Add(peer.rid, peer)
	if old != nil {
		old.Close()
	}
	return &answer, nil
}

func (r *Router) trickle(rid, uid string, candi string) error {
	var ici webrtc.ICECandidateInit
	err := json.Unmarshal([]byte(candi), &ici)
	if err != nil || ici.Candidate == "" {
		return err
	}
	p := r.engine.rooms.Get(rid, uid)
	if p == nil {
		return fmt.Errorf("peer %s not found in %s", uid, rid)
	}
	p.Lock()
	defer p.Unlock()

	return p.pc.AddICECandidate(ici)
}

func (r *Router) subscribe(rid, uid string) (*webrtc.SessionDescription, error) {
	peer := r.engine.rooms.Get(rid, uid)
	if peer == nil {
		return nil, fmt.Errorf("peer %s not found in %s", uid, rid)
	}

	var renegotiate bool
	r.engine.rooms.Iterate(rid, func(p *Peer) {
		peer.Lock()
		defer peer.Unlock()

		if p.uid == peer.uid {
			return
		}
		if peer.senders[p.cid] != nil {
			return
		}
		if p.track == nil {
			return
		}
		sender, err := peer.pc.AddTrack(p.track)
		if err != nil {
			logger.Printf("failed to add sender %s to peer %s\n", p.id(), peer.id())
			return
		}
		if id := sender.Track().ID(); id != p.cid {
			panic(fmt.Errorf("malformed peer and track id %s %s", p.cid, id))
		}
		peer.senders[p.cid] = sender
		renegotiate = true
	})
	if !renegotiate {
		return &webrtc.SessionDescription{}, nil
	}

	offer, err := peer.pc.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	err = peer.pc.SetLocalDescription(offer)
	return &offer, err
}

func (r *Router) answer(rid, uid string, ss string) error {
	var answer webrtc.SessionDescription
	err := json.Unmarshal([]byte(ss), &answer)
	if err != nil {
		return err
	}
	if answer.Type != webrtc.SDPTypeAnswer {
		return fmt.Errorf("invalid sdp type %s", answer.Type)
	}

	parser := sdp.SessionDescription{}
	err = parser.Unmarshal([]byte(answer.SDP))
	if err != nil {
		return err
	}

	peer := r.engine.rooms.Get(rid, uid)
	if peer == nil {
		return fmt.Errorf("peer %s not found in %s", uid, rid)
	}

	peer.Lock()
	defer peer.Unlock()
	return peer.pc.SetRemoteDescription(answer)
}

func validateId(id string) error {
	uid, err := url.QueryUnescape(id)
	if err != nil {
		return err
	}
	if eid := url.QueryEscape(uid); eid != id {
		return fmt.Errorf("unmatch %s %s", id, eid)
	}
	return nil
}
