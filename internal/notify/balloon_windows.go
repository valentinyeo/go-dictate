package notify

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/valen/go-dictate/internal/logger"
)

const createNoWindow = 0x08000000

func ShowBalloon(title, message string, duration time.Duration) {
	go func() {
		cmd := balloonCommand(title, message, duration)
		if err := cmd.Start(); err != nil {
			logger.Error("[Notify] failed to launch notification: %v", err)
		}
	}()
}

func balloonCommand(title, message string, duration time.Duration) *exec.Cmd {
	secs := int(duration.Seconds())
	if secs < 1 {
		secs = 1
	}
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.BalloonTipTitle = $env:GD_BALLOON_TITLE
$n.BalloonTipText = $env:GD_BALLOON_TEXT
$n.Visible = $true
$n.ShowBalloonTip(%d)
Start-Sleep %d
$n.Dispose()`,
		secs*1000,
		secs,
	)

	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command", script)
	cmd.Env = append(os.Environ(), "GD_BALLOON_TITLE="+flatten(title), "GD_BALLOON_TEXT="+flatten(message))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

func flatten(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
