package audio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeVolumeSession struct {
	name   string
	pid    uint32
	volume float32
	sets   int
	setErr error
}

func (s *fakeVolumeSession) Name() string          { return s.name }
func (s *fakeVolumeSession) PID() uint32           { return s.pid }
func (s *fakeVolumeSession) Get() (float32, error) { return s.volume, nil }
func (s *fakeVolumeSession) Set(volume float32) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.volume = volume
	s.sets++
	return nil
}

type fakeVolumeMixer struct{ sessions []volumeSession }

func (m *fakeVolumeMixer) WithSessions(fn func([]volumeSession) error) error {
	return fn(m.sessions)
}

func newTestDucker(t *testing.T, mixer volumeMixer) *appVolumeState {
	t.Helper()
	d, err := newAppVolumeState(0.05, mixer, filepath.Join(t.TempDir(), "ducked.json"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRestoreAfterDuckingSwitchedOff(t *testing.T) {
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}
	d := newTestDucker(t, &fakeVolumeMixer{sessions: []volumeSession{chrome}})
	duckEnabled := true
	if duckEnabled {
		if err := d.Duck(); err != nil {
			t.Fatal(err)
		}
	}
	duckEnabled = false
	if err := d.RestoreAfterSpeaking(duckEnabled); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 1 {
		t.Fatalf("Chrome volume = %v, want 1 after ducking switched off", chrome.volume)
	}
}

func TestRestoreWhenSessionPIDChanged(t *testing.T) {
	chrome := &fakeVolumeSession{name: "Chrome.EXE", pid: 1, volume: 1}
	mixer := &fakeVolumeMixer{sessions: []volumeSession{chrome}}
	d := newTestDucker(t, mixer)
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	chrome = &fakeVolumeSession{name: "chrome.exe", pid: 2, volume: 0.05}
	mixer.sessions = []volumeSession{chrome}
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 1 {
		t.Fatalf("Chrome PID 2 volume = %v, want 1", chrome.volume)
	}
}

func TestPendingRestoreRetriedNotOverwritten(t *testing.T) {
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}
	mixer := &fakeVolumeMixer{sessions: []volumeSession{chrome}}
	d := newTestDucker(t, mixer)
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	mixer.sessions = nil
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	chrome = &fakeVolumeSession{name: "chrome.exe", pid: 2, volume: 0.05}
	mixer.sessions = []volumeSession{chrome}
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 1 {
		t.Fatalf("Chrome volume = %v, want original 1 rather than re-recorded 0.05", chrome.volume)
	}
}

func TestCrashRecordRestoredOnStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ducked.json")
	if err := os.WriteFile(path, []byte(`{"chrome.exe":{"original":1,"ducked":0.05}}`), 0600); err != nil {
		t.Fatal(err)
	}
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 2, volume: 0.05}
	if _, err := newAppVolumeState(0.1, &fakeVolumeMixer{sessions: []volumeSession{chrome}}, path); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 1 {
		t.Fatalf("Chrome volume = %v, want 1 after crash recovery", chrome.volume)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("crash record still present: %v", err)
	}
}

func TestUserChangedVolumeKept(t *testing.T) {
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}
	d := newTestDucker(t, &fakeVolumeMixer{sessions: []volumeSession{chrome}})
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	chrome.volume = 0.4
	sets := chrome.sets
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 0.4 || chrome.sets != sets {
		t.Fatalf("Restore overwrote user volume: volume=%v sets=%d, want 0.4 and %d", chrome.volume, chrome.sets, sets)
	}
}

func TestRestoreAllAppSessions(t *testing.T) {
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}
	mixer := &fakeVolumeMixer{sessions: []volumeSession{chrome}}
	d := newTestDucker(t, mixer)
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	second := &fakeVolumeSession{name: "CHROME.EXE", pid: 2, volume: 0.05}
	mixer.sessions = []volumeSession{chrome, second}
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 1 || second.volume != 1 {
		t.Fatalf("session volumes = %v, %v, want 1, 1", chrome.volume, second.volume)
	}
}

func TestCrashRecordAbsentAppStaysPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ducked.json")
	mixer := &fakeVolumeMixer{sessions: []volumeSession{&fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}}}
	d, err := newAppVolumeState(0.05, mixer, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	mixer.sessions = nil
	d, err = newAppVolumeState(0.05, mixer, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("absent app's crash record was lost: %v", err)
	}
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 2, volume: 0.05}
	mixer.sessions = []volumeSession{chrome}
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 1 {
		t.Fatalf("Chrome volume = %v, want 1", chrome.volume)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("completed crash record still present: %v", err)
	}
}

func TestCloseRestoresAndPreventsReducking(t *testing.T) {
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}
	d := newTestDucker(t, &fakeVolumeMixer{sessions: []volumeSession{chrome}})
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 1 {
		t.Fatalf("Chrome volume = %v, want 1 after exit", chrome.volume)
	}
}

func TestRestoreWithinDuckTolerance(t *testing.T) {
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}
	d := newTestDucker(t, &fakeVolumeMixer{sessions: []volumeSession{chrome}})
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	chrome.volume = 0.059
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 1 {
		t.Fatalf("Chrome volume = %v, want 1 within duck tolerance", chrome.volume)
	}
}

func TestCrashRecoveryKeepsUserVolume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ducked.json")
	if err := os.WriteFile(path, []byte(`{"chrome.exe":{"original":1,"ducked":0.05}}`), 0600); err != nil {
		t.Fatal(err)
	}
	chrome := &fakeVolumeSession{name: "chrome.exe", pid: 2, volume: 0.4}
	if _, err := newAppVolumeState(0.05, &fakeVolumeMixer{sessions: []volumeSession{chrome}}, path); err != nil {
		t.Fatal(err)
	}
	if chrome.volume != 0.4 || chrome.sets != 0 {
		t.Fatalf("crash recovery overwrote user volume: volume=%v sets=%d", chrome.volume, chrome.sets)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("discarded crash record still present: %v", err)
	}
}

func TestUserChangedSessionDoesNotBlockOtherSessions(t *testing.T) {
	first := &fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}
	second := &fakeVolumeSession{name: "chrome.exe", pid: 2, volume: 1}
	d := newTestDucker(t, &fakeVolumeMixer{sessions: []volumeSession{first, second}})
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	first.volume = 0.4
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if first.volume != 0.4 || second.volume != 1 {
		t.Fatalf("session volumes = %v, %v, want 0.4, 1", first.volume, second.volume)
	}
}

func TestPartialRestoreFailureStaysPending(t *testing.T) {
	first := &fakeVolumeSession{name: "chrome.exe", pid: 1, volume: 1}
	second := &fakeVolumeSession{name: "chrome.exe", pid: 2, volume: 1}
	d := newTestDucker(t, &fakeVolumeMixer{sessions: []volumeSession{first, second}})
	if err := d.Duck(); err != nil {
		t.Fatal(err)
	}
	second.setErr = errors.New("session unavailable")
	if err := d.Restore(); err == nil {
		t.Fatal("Restore did not report the failed session")
	}
	if _, err := os.Stat(d.recordPath); err != nil {
		t.Fatalf("failed restore's record was lost: %v", err)
	}
	second.setErr = nil
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if first.volume != 1 || second.volume != 1 {
		t.Fatalf("session volumes = %v, %v, want 1, 1", first.volume, second.volume)
	}
	if _, err := os.Stat(d.recordPath); !os.IsNotExist(err) {
		t.Fatalf("completed restore record still present: %v", err)
	}
}
