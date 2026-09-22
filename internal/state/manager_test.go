package state

import "testing"

func TestPushToTalkCaptureLifecycle(t *testing.T) {
	m := NewManager()
	m.SetPushToTalk(true)
	if m.CaptureAllowed() {
		t.Fatal("idle PTT opened the microphone")
	}
	for _, automatic := range []AppState{Active, Speaking, AutoPaused, MediaPaused} {
		m.Set(automatic)
		if m.CaptureAllowed() {
			t.Fatalf("%v reopened idle PTT", automatic)
		}
	}
	m.Hold(true)
	m.Hold(true)
	if m.Get() != PushToTalkRecording || !m.CaptureAllowed() {
		t.Fatal("hold must record")
	}
	m.Hold(false)
	m.Hold(true)
	if m.Get() != PushToTalkFinishing || m.CaptureAllowed() {
		t.Fatal("release/repeat reopened capture while finishing")
	}
	m.FinishPushToTalk()
	if m.Get() != PushToTalkIdle || m.CaptureAllowed() {
		t.Fatal("finishing must return to mic-off standby")
	}
	m.SetPushToTalk(false)
	if !m.CaptureAllowed() {
		t.Fatal("continuous mode must resume capture")
	}
}

func TestPauseOverridesPushToTalk(t *testing.T) {
	m := NewManager()
	m.SetPushToTalk(true)
	m.Hold(true)
	m.TogglePause()
	m.Hold(false)
	m.Hold(true)
	m.FinishPushToTalk()
	if m.Get() != Paused || m.CaptureAllowed() {
		t.Fatal("a hold or late result overrode manual pause")
	}
	m.TogglePause()
	if m.Get() != PushToTalkIdle || m.CaptureAllowed() {
		t.Fatal("resume must wait for a new hold")
	}
	m.TogglePause()
	m.SetPushToTalk(false)
	m.SetPushToTalk(true)
	if m.Get() != Paused {
		t.Fatal("mode change overrode manual pause")
	}
}

func TestShortTapSurvivesCoalescedNotifications(t *testing.T) {
	m := NewManager()
	changed := m.Changed()
	m.SetPushToTalk(true)
	m.Hold(true)
	m.Hold(false)
	<-changed
	if m.Get() != PushToTalkFinishing || m.CaptureAllowed() {
		t.Fatal("short tap left capture active")
	}
	m.FinishPushToTalk()
	m.Hold(true)
	if !m.CaptureAllowed() {
		t.Fatal("next recording was blocked")
	}
}
