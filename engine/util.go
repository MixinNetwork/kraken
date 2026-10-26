package engine

import (
	"fmt"
	"time"

	"github.com/pion/webrtc/v4"
)

// waitForCompletion bounds a wait without launching background work.
func waitForCompletion(done <-chan struct{}, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
		return buildError(ErrorServerTimeout, fmt.Errorf("timeout after %s", duration))
	}
}

func setLocalDescription(pc *webrtc.PeerConnection, desc webrtc.SessionDescription, timeout time.Duration) error {
	code := ErrorServerSetLocalAnswer
	if desc.Type == webrtc.SDPTypeOffer {
		code = ErrorServerSetLocalOffer
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	err := pc.SetLocalDescription(desc)
	if err != nil {
		return buildError(code, err)
	}
	// Preserve the public timeout error instead of wrapping it as an SDP error.
	if err := waitForCompletion(gatherComplete, timeout); err != nil {
		return err
	}
	// Pion also completes the gathering promise when the connection closes.
	if pc.SignalingState() == webrtc.SignalingStateClosed {
		return buildError(code, webrtc.ErrConnectionClosed)
	}
	return nil
}
