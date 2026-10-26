package engine

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestWaitForCompletion(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			err := waitForCompletion(make(chan struct{}), 5*time.Second)
			assertErrorCode(t, err, ErrorServerTimeout)
			if time.Since(start) != 5*time.Second {
				t.Fatal("wait did not honor its deadline")
			}
			// synctest also checks that no worker goroutine remains blocked.
		})
	})
	t.Run("completed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				time.Sleep(time.Second)
				close(done)
			}()
			if err := waitForCompletion(done, 5*time.Second); err != nil {
				t.Fatal(err)
			}
		})
	})
}

func TestSetLocalDescriptionPreservesTimeout(t *testing.T) {
	for _, kind := range []webrtc.SDPType{webrtc.SDPTypeOffer, webrtc.SDPTypeAnswer} {
		t.Run(kind.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				pc, network := testGatheringConnection(t)
				defer network.unblock()
				var desc webrtc.SessionDescription
				var err error
				if kind == webrtc.SDPTypeOffer {
					if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
						t.Fatal(err)
					}
					desc, err = pc.CreateOffer(nil)
				} else {
					remote := testAudioConnection(t)
					if err := pc.SetRemoteDescription(*testOffer(t, remote, false)); err != nil {
						t.Fatal(err)
					}
					desc, err = pc.CreateAnswer(nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				network.blocked.Store(true)
				start := time.Now()
				err = setLocalDescription(pc, desc, 3*time.Second)
				assertErrorCode(t, err, ErrorServerTimeout)
				if time.Since(start) != 3*time.Second {
					t.Fatal("gathering did not honor its timeout")
				}
			})
		})
	}
}

func TestSetLocalDescriptionPreservesSDPErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		kind webrtc.SDPType
		code int
	}{
		{webrtc.SDPTypeOffer, ErrorServerSetLocalOffer},
		{webrtc.SDPTypeAnswer, ErrorServerSetLocalAnswer},
	} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			pc := testPeerConnection(t)
			if err := pc.Close(); err != nil {
				t.Fatal(err)
			}
			err := setLocalDescription(pc, webrtc.SessionDescription{Type: tc.kind}, time.Second)
			assertErrorCode(t, err, tc.code)
		})
	}
}
