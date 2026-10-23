package audio

import (
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/valen/go-dictate/internal/config"
	"github.com/valen/go-dictate/internal/logger"
)

type AppVolumeDucker struct {
	*appVolumeState
}

func NewAppVolumeDucker(duckVolume float32) *AppVolumeDucker {
	configPath, err := config.GetConfigPath()
	if err != nil {
		logger.Error("[AppVolumeDucker] Failed to locate crash record: %v", err)
		d, _ := newAppVolumeState(duckVolume, comVolumeMixer{}, "")
		d.loadErr = err
		return &AppVolumeDucker{d}
	}
	d, err := newAppVolumeState(duckVolume, comVolumeMixer{}, filepath.Join(filepath.Dir(configPath), "ducked.json"))
	if err != nil {
		logger.Error("[AppVolumeDucker] Failed to restore crash record: %v", err)
	}
	return &AppVolumeDucker{d}
}

type comVolumeMixer struct{}

type comVolumeSession struct {
	name   string
	pid    uint32
	volume uintptr
}

func (s *comVolumeSession) Name() string { return s.name }
func (s *comVolumeSession) PID() uint32  { return s.pid }

func (s *comVolumeSession) Get() (float32, error) {
	var volume float32
	ret, _, _ := syscall.Syscall(
		getVTable(s.volume, 4),
		2,
		s.volume,
		uintptr(unsafe.Pointer(&volume)),
		0,
	)
	if ret != 0 {
		return 0, fmt.Errorf("GetMasterVolume failed: HRESULT 0x%X", ret)
	}
	return volume, nil
}

func (s *comVolumeSession) Set(volume float32) error {
	ret, _, _ := syscall.Syscall(
		getVTable(s.volume, 3),
		3,
		s.volume,
		uintptr(math.Float32bits(volume)),
		0,
	)
	if ret != 0 {
		return fmt.Errorf("SetMasterVolume failed: HRESULT 0x%X", ret)
	}
	return nil
}

func (comVolumeMixer) WithSessions(fn func([]volumeSession) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ret, _, _ := coInitializeEx.Call(0, 0x0)
	if ret != 0 && ret != 0x00000001 {
		return fmt.Errorf("CoInitializeEx failed")
	}
	defer coUninitialize.Call()
	var deviceEnumerator uintptr
	ret, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
		0,
		CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&deviceEnumerator)),
	)
	if ret != 0 {
		return fmt.Errorf("CoCreateInstance failed")
	}
	defer syscall.Syscall(getVTable(deviceEnumerator, 2), 1, deviceEnumerator, 0, 0)
	var device uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(deviceEnumerator, 4),
		4,
		deviceEnumerator,
		uintptr(eRender),
		uintptr(eConsole),
		uintptr(unsafe.Pointer(&device)),
		0, 0,
	)
	if ret != 0 {
		return fmt.Errorf("GetDefaultAudioEndpoint failed")
	}
	defer syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)
	var sessionManager uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(device, 3),
		5,
		device,
		uintptr(unsafe.Pointer(&IID_IAudioSessionManager2)),
		CLSCTX_INPROC_SERVER,
		0,
		uintptr(unsafe.Pointer(&sessionManager)),
		0,
	)
	if ret != 0 {
		return fmt.Errorf("Activate failed")
	}
	defer syscall.Syscall(getVTable(sessionManager, 2), 1, sessionManager, 0, 0)
	var sessionEnumerator uintptr
	ret, _, _ = syscall.Syscall(
		getVTable(sessionManager, 5),
		2,
		sessionManager,
		uintptr(unsafe.Pointer(&sessionEnumerator)),
		0,
	)
	if ret != 0 {
		return fmt.Errorf("GetSessionEnumerator failed")
	}
	defer syscall.Syscall(getVTable(sessionEnumerator, 2), 1, sessionEnumerator, 0, 0)
	var sessionCount int32
	syscall.Syscall(
		getVTable(sessionEnumerator, 3),
		2,
		sessionEnumerator,
		uintptr(unsafe.Pointer(&sessionCount)),
		0,
	)

	var sessions []volumeSession
	defer func() {
		for _, session := range sessions {
			volume := session.(*comVolumeSession).volume
			syscall.Syscall(getVTable(volume, 2), 1, volume, 0, 0)
		}
	}()
	for i := int32(0); i < sessionCount; i++ {
		var session uintptr
		ret, _, _ = syscall.Syscall(
			getVTable(sessionEnumerator, 4),
			3,
			sessionEnumerator,
			uintptr(i),
			uintptr(unsafe.Pointer(&session)),
		)
		if ret != 0 || session == 0 {
			continue
		}
		var processID uint32
		ret, _, _ = syscall.Syscall(
			getVTable(session, 14),
			2,
			session,
			uintptr(unsafe.Pointer(&processID)),
			0,
		)
		if ret != 0 {
			syscall.Syscall(getVTable(session, 2), 1, session, 0, 0)
			continue
		}
		processName := getProcessName(processID)
		IID_ISimpleAudioVolume := syscall.GUID{
			Data1: 0x87CE5498,
			Data2: 0x68D6,
			Data3: 0x44E5,
			Data4: [8]byte{0x92, 0x15, 0x6D, 0xA4, 0x7E, 0xF8, 0x83, 0xD8},
		}
		var simpleVolume uintptr
		ret, _, _ = syscall.Syscall6(
			getVTable(session, 0),
			3,
			session,
			uintptr(unsafe.Pointer(&IID_ISimpleAudioVolume)),
			uintptr(unsafe.Pointer(&simpleVolume)),
			0, 0, 0,
		)
		syscall.Syscall(getVTable(session, 2), 1, session, 0, 0)
		if ret != 0 || simpleVolume == 0 {
			continue
		}
		sessions = append(sessions, &comVolumeSession{name: processName, pid: processID, volume: simpleVolume})
	}
	return fn(sessions)
}
