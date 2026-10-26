package engine

import (
	"testing"
	"testing/synctest"
	"time"
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
