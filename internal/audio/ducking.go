package audio

// AudioDucker is a stub implementation for audio ducking
// In the Rust version, this feature is disabled, so we just provide
// a no-op implementation for future expansion
type AudioDucker struct {
	duckVolume float32
}

func NewAudioDucker(duckVolume float32) *AudioDucker {
	return &AudioDucker{
		duckVolume: duckVolume,
	}
}

func (d *AudioDucker) Duck() error {
	// No-op: audio ducking disabled
	return nil
}

func (d *AudioDucker) Restore() error {
	// No-op: audio ducking disabled
	return nil
}
