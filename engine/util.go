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
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	err := pc.SetLocalDescription(desc)
	if err != nil {
		return err
	}
	return waitForCompletion(gatherComplete, timeout)
}
