package audio

import "sync"

type volumeSession interface {
	Name() string
	PID() uint32
	Get() (float32, error)
	Set(float32) error
}

type volumeMixer interface {
	WithSessions(func([]volumeSession) error) error
}

type appVolumeState struct {
	duckVolume      float32
	mixer           volumeMixer
	recordPath      string
	originalVolumes map[uint32]float32
	isDucked        bool
	mu              sync.Mutex
}

func newAppVolumeState(duckVolume float32, mixer volumeMixer, recordPath string) (*appVolumeState, error) {
	return &appVolumeState{duckVolume: duckVolume, mixer: mixer, recordPath: recordPath, originalVolumes: make(map[uint32]float32)}, nil
}

func (d *appVolumeState) Duck() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.isDucked {
		return nil
	}
	return d.mixer.WithSessions(func(sessions []volumeSession) error {
		for _, session := range sessions {
			volume, err := session.Get()
			if err != nil {
				return err
			}
			d.originalVolumes[session.PID()] = volume
			if err := session.Set(d.duckVolume); err != nil {
				return err
			}
			d.isDucked = true
		}
		return nil
	})
}

func (d *appVolumeState) Restore() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isDucked {
		return nil
	}
	return d.mixer.WithSessions(func(sessions []volumeSession) error {
		for _, session := range sessions {
			if volume, ok := d.originalVolumes[session.PID()]; ok {
				if err := session.Set(volume); err != nil {
					return err
				}
			}
		}
		d.originalVolumes = make(map[uint32]float32)
		d.isDucked = false
		return nil
	})
}

func (d *appVolumeState) RestoreAfterSpeaking(duckEnabled bool) error {
	if !duckEnabled {
		return nil
	}
	return d.Restore()
}
