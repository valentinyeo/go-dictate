package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Logger struct {
	file *os.File
	mu   sync.Mutex
}

var instance *Logger
var once sync.Once

func Init() error {
	var err error
	once.Do(func() {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			err = fmt.Errorf("APPDATA not set")
			return
		}

		logDir := filepath.Join(appData, "dictation")
		if err = os.MkdirAll(logDir, 0755); err != nil {
			return
		}

		logPath := filepath.Join(logDir, "debug.log")

		// Open in append mode, create if doesn't exist
		instance = &Logger{}
		instance.file, err = os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}

		// Write session start marker
		instance.write("======================================")
		instance.write(fmt.Sprintf("SESSION START: %s", time.Now().Format("2006-01-02 15:04:05")))
		instance.write("======================================")
	})
	return err
}

func (l *Logger) write(msg string) {
	if l.file == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(l.file, "[%s] %s\n", timestamp, msg)
	l.file.Sync() // Flush immediately
}

func Info(format string, args ...interface{}) {
	if instance == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	instance.write("[INFO] " + msg)
	fmt.Println(msg) // Also print to console
}

func Error(format string, args ...interface{}) {
	if instance == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	instance.write("[ERROR] " + msg)
	fmt.Println(msg) // Also print to console
}

func Debug(format string, args ...interface{}) {
	if instance == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	instance.write("[DEBUG] " + msg)
}

func Transcript(text string) {
	if instance == nil {
		return
	}
	instance.write(fmt.Sprintf("[TRANSCRIPT] Received: '%s'", text))
}

func Typed(text string) {
	if instance == nil {
		return
	}
	instance.write(fmt.Sprintf("[KEYBOARD] Typed: '%s'", text))
}

func Close() {
	if instance != nil && instance.file != nil {
		instance.write("SESSION END")
		instance.file.Close()
	}
}

// RedirectStderr redirects stderr (panic output) to a crash log file.
// Since we build with -H windowsgui, panics go nowhere and the app dies silently.
// This captures them so we can diagnose crashes.
func RedirectStderr() {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return
	}
	crashPath := filepath.Join(appData, "dictation", "crash.log")
	f, err := os.OpenFile(crashPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	// Set the Windows stderr handle to our file
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setStdHandle := kernel32.NewProc("SetStdHandle")
	const STD_ERROR_HANDLE = ^uintptr(12 - 1) // 0xFFFFFFF4 = (DWORD)-12
	setStdHandle.Call(STD_ERROR_HANDLE, f.Fd())
	// Also set os.Stderr so fmt.Fprintf(os.Stderr, ...) works
	os.Stderr = f
}
