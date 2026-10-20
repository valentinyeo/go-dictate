package main

import "time"

func persistentCloseReason(now, started, lastSpeech, lastAudio time.Time, speaking, micReleased bool, maxAge time.Duration) string {
	if started.IsZero() || speaking {
		return ""
	}
	if micReleased {
		return "mic_released"
	}
	if now.Sub(started) > maxAge {
		return "max_age"
	}
	lastActivity := started
	if lastSpeech.After(lastActivity) {
		lastActivity = lastSpeech
	}
	if lastAudio.After(lastActivity) {
		lastActivity = lastAudio
	}
	if now.Sub(lastActivity) >= 30*time.Second {
		return "idle"
	}
	return ""
}
