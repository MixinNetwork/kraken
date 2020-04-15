package engine

import (
	"encoding/json"
	"fmt"

	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

type Router struct {
	engine *Engine
}

func NewRouter(engine *Engine) *Router {
	return &Router{engine: engine}
}

func (r *Router) rpcList(params []interface{}) ([]string, error) {
	return nil, nil
}

func (r *Router) rpcPublish(params []interface{}) (*webrtc.SessionDescription, error) {
	if len(params) != 3 {
		return nil, fmt.Errorf("invalid params count %d", len(params))
	}
	rid, ok := params[0].(string)
	if !ok {
		return nil, fmt.Errorf("invalid rid type %s", params[0])
	}
	pid, ok := params[1].(string)
	if !ok {
		return nil, fmt.Errorf("invalid pid type %s", params[1])
	}
	sdp, ok := params[2].(string)
	if !ok {
		return nil, fmt.Errorf("invalid sdp type %s", params[2])
	}
	return r.publish(rid, pid, sdp)
}

func (r *Router) rpcTrickle(params []interface{}) error {
	if len(params) != 3 {
		return fmt.Errorf("invalid params count %d", len(params))
	}
	rid, ok := params[0].(string)
	if !ok {
		return fmt.Errorf("invalid rid type %s", params[0])
	}
	pid, ok := params[1].(string)
	if !ok {
		return fmt.Errorf("invalid pid type %s", params[1])
	}
	candi, ok := params[2].(string)
	if !ok {
		return fmt.Errorf("invalid candi type %s", params[2])
	}
	return r.trickle(rid, pid, candi)
}

func (r *Router) rpcSubscribe(params []interface{}) (string, error) {
	if len(params) != 3 {
		return "", fmt.Errorf("invalid params count %d", len(params))
	}
	rid, ok := params[0].(string)
	if !ok {
		return "", fmt.Errorf("invalid rid type %s", params[0])
	}
	pid, ok := params[1].(string)
	if !ok {
		return "", fmt.Errorf("invalid pid type %s", params[1])
	}
	sid, ok := params[2].(string)
	if !ok {
		return "", fmt.Errorf("invalid sid type %s", params[2])
	}
	return r.subscribe(rid, pid, sid)
}

func (r *Router) publish(rid, pid string, ss string) (*webrtc.SessionDescription, error) {
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

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RtpTransceiverInit{
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
	r.engine.AddPeer(rid, pid, pc)
	return &answer, nil
}

func (r *Router) trickle(rid, pid string, candi string) error {
	var ici webrtc.ICECandidateInit
	err := json.Unmarshal([]byte(candi), &ici)
	if err != nil || ici.Candidate == "" {
		return err
	}
	p := r.engine.rooms.Get(rid, pid)
	if p == nil {
		return fmt.Errorf("peer %s not found in %s", pid, rid)
	}
	return p.pc.AddICECandidate(ici)
}

func (r *Router) subscribe(rid, pid, sid string) (string, error) {
	p := r.engine.rooms.Get(rid, pid)
	if p == nil {
		return "", fmt.Errorf("peer %s not found in %s", pid, rid)
	}
	s := r.engine.rooms.Get(rid, sid)
	if s == nil {
		return "", fmt.Errorf("peer %s not found in %s", sid, rid)
	}

	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	err = p.pc.SetLocalDescription(offer)
	if err != nil {
		return "", err
	}
	p.subscribers = append(p.subscribers, s)
	return offer.SDP, nil
}
