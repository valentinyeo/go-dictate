package notify

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/valen/go-dictate/internal/logger"
)

const createNoWindow = 0x08000000

func ShowBalloon(title, message string, duration time.Duration) {
	go func() {
		secs := int(duration.Seconds())
		if secs < 1 {
			secs = 1
		}
		script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.BalloonTipTitle = '%s'
$n.BalloonTipText = '%s'
$n.Visible = $true
$n.ShowBalloonTip(%d)
Start-Sleep %d
$n.Dispose()`,
			escapePSString(title),
			escapePSString(message),
			secs*1000,
			secs,
		)

		cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		if err := cmd.Start(); err != nil {
			logger.Error("[Notify] failed to launch notification: %v", err)
		}
	}()
}

func escapePSString(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			result = append(result, '\'', '\'')
		case '\n', '\r':
			result = append(result, ' ')
		default:
			result = append(result, s[i])
		}
	}
	return string(result)
}
