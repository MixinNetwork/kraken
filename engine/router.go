package engine

import (
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

func (r *Router) rpcJoin(params []interface{}) (string, error) {
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
	sdp, ok := params[2].(string)
	if !ok {
		return "", fmt.Errorf("invalid sdp type %s", params[2])
	}
	return r.join(rid, pid, sdp)
}

func (r *Router) join(rid, pid string, jsep string) (string, error) {
	sdp := sdp.SessionDescription{}
	err := sdp.Unmarshal([]byte(jsep))
	if err != nil {
		return "", err
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
		return "", err
	}

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: jsep}
	err = pc.SetRemoteDescription(offer)
	if err != nil {
		pc.Close()
		return "", err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return "", err
	}

	err = pc.SetLocalDescription(answer)
	if err != nil {
		pc.Close()
		return "", err
	}
	r.engine.AddPeer(rid, pid, pc)
	return answer.SDP, err
}
