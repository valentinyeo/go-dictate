package main

import (
	"testing"
	"time"
)

func TestIdleCloseMicReleased(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if got := persistentCloseReason(start.Add(time.Second), start, start, start, false, true, 90*time.Second); got != "mic_released" {
		t.Fatalf("reason = %q, want mic_released", got)
	}
}

func TestIdleCloseAfter30Seconds(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if got := persistentCloseReason(start.Add(30*time.Second), start, start, start, false, false, 90*time.Second); got != "idle" {
		t.Fatalf("reason = %q, want idle", got)
	}
}

func TestIdleCloseNotAt29Seconds(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if got := persistentCloseReason(start.Add(29*time.Second), start, start, start, false, false, 90*time.Second); got != "" {
		t.Fatalf("reason = %q, want no close", got)
	}
}

func TestIdleCloseNeverWhileSpeaking(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, released := range []bool{false, true} {
		if got := persistentCloseReason(start.Add(2*time.Minute), start, start, start, true, released, 90*time.Second); got != "" {
			t.Fatalf("released=%t: reason = %q, want no close", released, got)
		}
	}
}

func TestIdleCloseMaxAge(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	now := start.Add(91 * time.Second)
	if got := persistentCloseReason(now, start, now, now, false, false, 90*time.Second); got != "max_age" {
		t.Fatalf("reason = %q, want max_age", got)
	}
}

func TestIdleCloseMaxAgeDeferredUntilSilence(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	now := start.Add(91 * time.Second)
	if got := persistentCloseReason(now, start, now, now, true, false, 90*time.Second); got != "" {
		t.Fatalf("during speech: reason = %q, want no close", got)
	}
	if got := persistentCloseReason(now, start, now, now, false, false, 90*time.Second); got != "max_age" {
		t.Fatalf("after silence: reason = %q, want max_age", got)
	}
}

func TestIdleCloseActivityRestartsIdle(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		lastSpeech time.Time
		lastAudio  time.Time
	}{
		{"speech", start.Add(10 * time.Second), start},
		{"audio", start, start.Add(10 * time.Second)},
		{"new connection", time.Time{}, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activity := start
			if tt.lastSpeech.After(activity) {
				activity = tt.lastSpeech
			}
			if tt.lastAudio.After(activity) {
				activity = tt.lastAudio
			}
			if got := persistentCloseReason(activity.Add(29*time.Second), start, tt.lastSpeech, tt.lastAudio, false, false, 90*time.Second); got != "" {
				t.Fatalf("reason = %q, want no close", got)
			}
			if got := persistentCloseReason(activity.Add(30*time.Second), start, tt.lastSpeech, tt.lastAudio, false, false, 90*time.Second); got != "idle" {
				t.Fatalf("reason = %q, want idle", got)
			}
		})
	}
}

func TestIdleCloseNoConnection(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if got := persistentCloseReason(now, time.Time{}, now, now, false, true, 90*time.Second); got != "" {
		t.Fatalf("reason = %q, want no close", got)
	}
}

func TestIdleCloseMaxAgeBoundary(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	now := start.Add(90 * time.Second)
	if got := persistentCloseReason(now, start, now, now, false, false, 90*time.Second); got != "" {
		t.Fatalf("reason = %q, want no close at exact age cap", got)
	}
}
