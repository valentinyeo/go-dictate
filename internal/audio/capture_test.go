package audio

import (
	"os"
	"testing"
	"time"
)

// Opt-in hardware smoke test. No audio is stored or sent to a speech service.
func TestCaptureReleasesDevice(t *testing.T) {
	if os.Getenv("GO_DICTATE_TEST_MIC") != "1" {
		t.Skip("set GO_DICTATE_TEST_MIC=1 to test the Windows microphone")
	}
	for i := 0; i < 2; i++ {
		chunks := make(chan []int16, 100)
		capture, err := NewCapture(chunks, nil)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-chunks:
		case <-time.After(3 * time.Second):
			capture.Stop()
			t.Fatal("no audio callbacks")
		}
		capture.Stop()
		if capture.device != nil || capture.ctx != nil {
			t.Fatal("capture retained device or context after Stop")
		}
		last := capture.lastCallbackNano.Load()
		time.Sleep(200 * time.Millisecond)
		if capture.lastCallbackNano.Load() != last {
			t.Fatal("audio callbacks continued after Stop")
		}
		capture.Stop() // repeated release must be harmless
	}
}
