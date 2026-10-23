package audio

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/valen/go-dictate/internal/logger"
)

type volumeSession interface {
	Name() string
	PID() uint32
	Get() (float32, error)
	Set(float32) error
}

type volumeMixer interface {
	WithSessions(func([]volumeSession) error) error
}

type duckedVolume struct {
	Original float32 `json:"original"`
	Ducked   float32 `json:"ducked"`
}

type appVolumeState struct {
	duckVolume      float32
	mixer           volumeMixer
	recordPath      string
	originalVolumes map[string]duckedVolume
	activeApps      map[string]bool
	loadErr         error
	closed          bool
	mu              sync.Mutex
}

var mediaAppProcesses = []string{
	"chrome.exe",
	"msedge.exe",
	"firefox.exe",
	"brave.exe",
	"opera.exe",
	"vlc.exe",
	"screenbox.exe",
	"spotify.exe",
}

func newAppVolumeState(duckVolume float32, mixer volumeMixer, recordPath string) (*appVolumeState, error) {
	d := &appVolumeState{
		duckVolume:      duckVolume,
		mixer:           mixer,
		recordPath:      recordPath,
		originalVolumes: make(map[string]duckedVolume),
		activeApps:      make(map[string]bool),
	}
	data, err := os.ReadFile(recordPath)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &d.originalVolumes)
	}
	if err != nil {
		d.loadErr = err
		return d, err
	}
	if d.originalVolumes == nil {
		d.originalVolumes = make(map[string]duckedVolume)
	}
	return d, d.Restore()
}

func (d *appVolumeState) Duck() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	if d.loadErr != nil {
		return d.loadErr
	}
	logger.Info("[AppVolumeDucker] Ducking media apps to %.0f%%", d.duckVolume*100)
	return d.mixer.WithSessions(func(sessions []volumeSession) error {
		if err := d.restoreSessions(sessions, true); err != nil {
			return err
		}
		apps := make(map[string][]volumeSession)
		for _, session := range sessions {
			name := strings.ToLower(session.Name())
			for _, mediaName := range mediaAppProcesses {
				if name == mediaName {
					apps[name] = append(apps[name], session)
					break
				}
			}
		}
		var result error
		duckedCount := 0
		for name, appSessions := range apps {
			if _, exists := d.originalVolumes[name]; exists {
				continue
			}
			original, err := appSessions[0].Get()
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			d.originalVolumes[name] = duckedVolume{Original: original, Ducked: d.duckVolume}
			if err := d.persist(); err != nil {
				return errors.Join(result, err)
			}
			d.activeApps[name] = true
			for _, session := range appSessions {
				if err := session.Set(d.duckVolume); err != nil {
					result = errors.Join(result, err)
					continue
				}
				actual, err := session.Get()
				result = errors.Join(result, err)
				logger.Info("[AppVolumeDucker] Ducked %s (PID %d): %.0f%% → %.0f%% (actual: %.0f%%)",
					session.Name(), session.PID(), original*100, d.duckVolume*100, actual*100)
				duckedCount++
			}
		}
		logger.Info("[AppVolumeDucker] ✅ Ducked %d media app(s)", duckedCount)
		return result
	})
}

func (d *appVolumeState) Restore() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.restore()
}

func (d *appVolumeState) restore() error {
	clear(d.activeApps)
	if d.loadErr != nil {
		return d.loadErr
	}
	if len(d.originalVolumes) == 0 {
		return d.persist()
	}
	logger.Info("[AppVolumeDucker] Restoring media app volumes (stored %d apps)", len(d.originalVolumes))
	return d.mixer.WithSessions(func(sessions []volumeSession) error {
		return d.restoreSessions(sessions, false)
	})
}

func (d *appVolumeState) restoreSessions(sessions []volumeSession, pendingOnly bool) error {
	var result error
	for name, volume := range d.originalVolumes {
		if pendingOnly && d.activeApps[name] {
			continue
		}
		found := false
		var appErr error
		for _, session := range sessions {
			if strings.ToLower(session.Name()) != name {
				continue
			}
			found = true
			current, err := session.Get()
			if err != nil {
				appErr = errors.Join(appErr, err)
				continue
			}
			if math.Abs(float64(current)-float64(volume.Ducked)) > 0.01 {
				logger.Info("[AppVolumeDucker] Keeping user-changed volume for %s (PID %d)", session.Name(), session.PID())
				continue
			}
			if err := session.Set(volume.Original); err != nil {
				appErr = errors.Join(appErr, err)
				continue
			}
			actual, err := session.Get()
			appErr = errors.Join(appErr, err)
			logger.Info("[AppVolumeDucker] Restored %s (PID %d): %.0f%% → %.0f%% (actual: %.0f%%)",
				session.Name(), session.PID(), current*100, volume.Original*100, actual*100)
		}
		if !found || appErr != nil {
			result = errors.Join(result, appErr)
			continue
		}
		delete(d.originalVolumes, name)
		delete(d.activeApps, name)
		if err := d.persist(); err != nil {
			return errors.Join(result, err)
		}
	}
	return result
}

func (d *appVolumeState) persist() error {
	if len(d.originalVolumes) == 0 {
		err := os.Remove(d.recordPath)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data, err := json.Marshal(d.originalVolumes)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(d.recordPath), "ducked-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), d.recordPath)
}

func (d *appVolumeState) RestoreAfterSpeaking(_ bool) error {
	err := d.Restore()
	if err != nil {
		logger.Error("[AppVolumeDucker] Failed to restore: %v", err)
	}
	return err
}

func (d *appVolumeState) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if err := d.restore(); err != nil {
		logger.Error("[AppVolumeDucker] Failed to restore on exit: %v", err)
	}
}
