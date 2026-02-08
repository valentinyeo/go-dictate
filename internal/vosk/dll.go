package vosk

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

var (
	dllMu      sync.Mutex
	dllChecked bool
	dllLoadErr error
	voskDLL    *syscall.DLL

	procModelNew       *syscall.Proc
	procModelFree      *syscall.Proc
	procRecognizerNew  *syscall.Proc
	procRecognizerFree *syscall.Proc
	procAcceptWaveform *syscall.Proc
	procResult         *syscall.Proc
	procFinalResult    *syscall.Proc
	procSetLogLevel    *syscall.Proc
)

func loadDLL() error {
	dllMu.Lock()
	defer dllMu.Unlock()

	if dllChecked {
		return dllLoadErr
	}
	dllChecked = true

	dllPath := findDLL()
	dll, err := syscall.LoadDLL(dllPath)
	if err != nil {
		dllLoadErr = fmt.Errorf("libvosk.dll not found: %w", err)
		return dllLoadErr
	}
	voskDLL = dll

	type procEntry struct {
		name string
		dest **syscall.Proc
	}
	procs := []procEntry{
		{"vosk_model_new", &procModelNew},
		{"vosk_model_free", &procModelFree},
		{"vosk_recognizer_new", &procRecognizerNew},
		{"vosk_recognizer_free", &procRecognizerFree},
		{"vosk_recognizer_accept_waveform", &procAcceptWaveform},
		{"vosk_recognizer_result", &procResult},
		{"vosk_recognizer_final_result", &procFinalResult},
		{"vosk_set_log_level", &procSetLogLevel},
	}
	for _, p := range procs {
		proc, err := dll.FindProc(p.name)
		if err != nil {
			dllLoadErr = fmt.Errorf("vosk proc %s not found: %w", p.name, err)
			return dllLoadErr
		}
		*p.dest = proc
	}

	dllLoadErr = nil
	return nil
}

// ResetDLL allows re-checking DLL availability after downloading it.
func ResetDLL() {
	dllMu.Lock()
	defer dllMu.Unlock()
	dllChecked = false
	dllLoadErr = nil
	voskDLL = nil
}

func findDLL() string {
	exe, err := os.Executable()
	if err == nil {
		dllPath := filepath.Join(filepath.Dir(exe), "libvosk.dll")
		if _, err := os.Stat(dllPath); err == nil {
			return dllPath
		}
	}
	return "libvosk.dll"
}

// IsAvailable checks if the Vosk DLL can be loaded.
func IsAvailable() bool {
	return loadDLL() == nil
}

// Model wraps a Vosk model pointer.
type Model struct {
	ptr uintptr
}

// Recognizer wraps a Vosk recognizer pointer.
type Recognizer struct {
	ptr uintptr
}

// NewModel loads a Vosk model from the given directory.
func NewModel(path string) (*Model, error) {
	if err := loadDLL(); err != nil {
		return nil, err
	}

	cpath, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}

	ret, _, _ := procModelNew.Call(uintptr(unsafe.Pointer(cpath)))
	if ret == 0 {
		return nil, fmt.Errorf("vosk_model_new failed for: %s", path)
	}

	return &Model{ptr: ret}, nil
}

// Free releases the model resources.
func (m *Model) Free() {
	if m.ptr != 0 {
		procModelFree.Call(m.ptr)
		m.ptr = 0
	}
}

// NewRecognizer creates a new recognizer for the given model and sample rate.
func NewRecognizer(model *Model, sampleRate float32) (*Recognizer, error) {
	if err := loadDLL(); err != nil {
		return nil, err
	}

	// Pass float32 bits as uintptr; Go's asmstdcall copies to both integer and XMM registers
	bits := math.Float32bits(sampleRate)
	ret, _, _ := procRecognizerNew.Call(model.ptr, uintptr(bits))
	if ret == 0 {
		return nil, fmt.Errorf("vosk_recognizer_new failed")
	}

	return &Recognizer{ptr: ret}, nil
}

// AcceptWaveform feeds audio data to the recognizer.
// Returns true when a final result is available.
func (r *Recognizer) AcceptWaveform(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	ret, _, _ := procAcceptWaveform.Call(
		r.ptr,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
	)
	return ret != 0
}

// Result returns the final result JSON for the current utterance.
func (r *Recognizer) Result() string {
	ret, _, _ := procResult.Call(r.ptr)
	return goStringFromCString(ret)
}

// FinalResult forces end-of-utterance and returns the final result.
func (r *Recognizer) FinalResult() string {
	ret, _, _ := procFinalResult.Call(r.ptr)
	return goStringFromCString(ret)
}

// Free releases the recognizer resources.
func (r *Recognizer) Free() {
	if r.ptr != 0 {
		procRecognizerFree.Call(r.ptr)
		r.ptr = 0
	}
}

// SetLogLevel sets the Vosk log level (-1 = silent).
func SetLogLevel(level int) {
	if loadDLL() != nil {
		return
	}
	procSetLogLevel.Call(uintptr(level))
}

func goStringFromCString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	var length int
	for {
		b := *(*byte)(unsafe.Pointer(ptr + uintptr(length)))
		if b == 0 {
			break
		}
		length++
		if length > 1<<20 {
			break
		}
	}
	return string(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), length))
}
