package engine

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"time"

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

func (r *Router) publish(rid, uid string, ss string) (string, *webrtc.SessionDescription, error) {
	if err := validateId(rid); err != nil {
		return "", nil, fmt.Errorf("invalid rid format %s %s", rid, err.Error())
	}
	if err := validateId(uid); err != nil {
		return "", nil, fmt.Errorf("invalid uid format %s %s", uid, err.Error())
	}
	var offer webrtc.SessionDescription
	err := json.Unmarshal([]byte(ss), &offer)
	if err != nil {
		return "", nil, err
	}
	if offer.Type != webrtc.SDPTypeOffer {
		return "", nil, fmt.Errorf("invalid sdp type %s", offer.Type)
	}

	parser := sdp.SessionDescription{}
	err = parser.Unmarshal([]byte(offer.SDP))
	if err != nil {
		return "", nil, err
	}

	room := r.engine.GetRoom(rid)
	room.Lock()
	defer room.Unlock()

	se := webrtc.SettingEngine{}
	se.SetLite(true)
	se.SetTrickle(false)
	se.SetInterfaceFilter(func(in string) bool { return in == r.engine.Interface })
	se.SetNAT1To1IPs([]string{r.engine.IP}, webrtc.ICECandidateTypeHost)
	se.SetConnectionTimeout(5*time.Second, 5*time.Second)

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
		return "", nil, err
	}

	peer := r.engine.BuildPeer(rid, uid, pc)
	track, err := pc.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), peer.cid, peer.uid)
	if err != nil {
		return "", nil, err
	}
	_, err = pc.AddTransceiverFromTrack(track, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendrecv,
	})
	if err != nil {
		return "", nil, err
	}

	err = pc.SetRemoteDescription(offer)
	if err != nil {
		pc.Close()
		return "", nil, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return "", nil, err
	}

	err = pc.SetLocalDescription(answer)
	if err != nil {
		pc.Close()
		return "", nil, err
	}
	old := room.m[peer.uid]
	if old != nil {
		old.Close()
	}
	room.m[peer.uid] = peer
	return peer.cid, &answer, nil
}

func (r *Router) trickle(rid, uid, cid string, candi string) error {
	var ici webrtc.ICECandidateInit
	err := json.Unmarshal([]byte(candi), &ici)
	if err != nil || ici.Candidate == "" {
		return err
	}

	room := r.engine.GetRoom(rid)
	room.Lock()
	peer, err := room.get(uid, cid)
	room.Unlock()
	if err != nil {
		return err
	}
	peer.Lock()
	defer peer.Unlock()

	return peer.pc.AddICECandidate(ici)
}

func (r *Router) subscribe(rid, uid, cid string) (*webrtc.SessionDescription, error) {
	room := r.engine.GetRoom(rid)
	room.Lock()
	defer room.Unlock()

	peer, err := room.get(uid, cid)
	if err != nil {
		return nil, err
	}
	peer.Lock()
	defer peer.Unlock()

	var renegotiate bool
	for _, p := range room.m {
		if p.uid == peer.uid {
			continue
		}
		p.Lock()
		old := peer.senders[p.uid]
		cid, track := p.cid, p.track

		if old != nil && (track == nil || old.id != cid) {
			err := peer.pc.RemoveTrack(old.rtp)
			if err != nil {
				logger.Printf("failed to remove sender %s from peer %s with error %s\n", p.id(), peer.id(), err.Error())
			} else {
				delete(peer.senders, p.uid)
				renegotiate = true
			}
		} else if track != nil && (old == nil || old.id != cid) {
			sender, err := peer.pc.AddTrack(track)
			if err != nil {
				logger.Printf("failed to add sender %s to peer %s with error %s\n", p.id(), peer.id(), err.Error())
			} else if id := sender.Track().ID(); id != cid {
				panic(fmt.Errorf("malformed peer and track id %s %s", cid, id))
			} else {
				peer.senders[p.uid] = &Sender{id: cid, rtp: sender}
				renegotiate = true
			}
		}
		p.Unlock()
	}
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

func (r *Router) answer(rid, uid, cid string, ss string) error {
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

	room := r.engine.GetRoom(rid)
	room.Lock()
	peer, err := room.get(uid, cid)
	room.Unlock()
	if err != nil {
		return err
	}
	peer.Lock()
	defer peer.Unlock()

	return peer.pc.SetRemoteDescription(answer)
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
