package engine

import (
	"github.com/pion/webrtc/v2"
)

type Router struct {
	engine *Engine
}

func (r *Router) join(rid, pid string, sdp string) (string, error) {
	se := webrtc.SettingEngine{}
	se.SetLite(true)
	se.SetTrickle(true)
	se.SetNAT1To1IPs([]string{r.engine.IP, r.engine.IP}, webrtc.ICECandidateTypeHost)
	se.SetInterfaceFilter(func(in string) bool { return in == r.engine.Interface })

	me := webrtc.MediaEngine{}
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}
	err := me.PopulateFromSDP(offer)
	if err != nil {
		return "", err
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(se))

	pcConfig := webrtc.Configuration{
		BundlePolicy:  webrtc.BundlePolicyMaxBundle,
		RTCPMuxPolicy: webrtc.RTCPMuxPolicyRequire,
	}
	pc, err := api.NewPeerConnection(pcConfig)
	if err != nil {
		return "", err
	}

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
