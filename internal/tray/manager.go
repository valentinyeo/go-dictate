package tray

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/getlantern/systray"
	"github.com/valen/go-dictate/internal/config"
	"github.com/valen/go-dictate/internal/state"
	"golang.org/x/sys/windows/registry"
)

type Manager struct {
	stateManager *state.Manager
	config       *config.Config
	configMu     sync.RWMutex

	pauseItem     *systray.MenuItem
	languageItem  *systray.MenuItem
	autostartItem *systray.MenuItem
	settingsItem  *systray.MenuItem
	logItem       *systray.MenuItem
	dashboardItem *systray.MenuItem
	restartItem   *systray.MenuItem
	exitItem      *systray.MenuItem

	ctx    context.Context
	cancel context.CancelFunc
}

func NewManager(stateManager *state.Manager, cfg *config.Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		stateManager: stateManager,
		config:       cfg,
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (m *Manager) Run() {
	systray.Run(m.onReady, m.onExit)
}

func (m *Manager) onReady() {
	// Set initial icon
	m.updateIcon(m.stateManager.Get())
	systray.SetTooltip("Dictation App v1.3.0")

	// Create menu items
	systray.AddMenuItem("v1.3.0 - Auto-Ducking", "Current version").Disable()
	systray.AddSeparator()
	m.languageItem = systray.AddMenuItem("Language: English", "Toggle language")
	m.autostartItem = systray.AddMenuItemCheckbox("Start on Boot", "Start application on boot", isAutostartEnabled())
	systray.AddSeparator()
	m.settingsItem = systray.AddMenuItem("Open Config File", "Open configuration file")
	m.logItem = systray.AddMenuItem("Open Debug Log", "Open debug log file")
	m.dashboardItem = systray.AddMenuItem("Open Deepgram Dashboard", "Open Deepgram console")
	systray.AddSeparator()
	m.pauseItem = systray.AddMenuItem("⏸ Pause / Resume", "Ctrl+Alt+X: Pause or resume dictation")
	m.restartItem = systray.AddMenuItem("🔄 Restart", "Restart the application")
	m.exitItem = systray.AddMenuItem("Exit", "Exit application")

	// Update language menu based on config
	m.updateLanguageMenu()

	// Start event handlers
	go m.handlePauseClick()
	go m.handleLanguageClick()
	go m.handleAutostartClick()
	go m.handleSettingsClick()
	go m.handleLogClick()
	go m.handleDashboardClick()
	go m.handleRestartClick()
	go m.handleExitClick()
	go m.handleIconUpdates()
}

func (m *Manager) onExit() {
	m.cancel()
}

func (m *Manager) handlePauseClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.pauseItem.ClickedCh:
			newState := m.stateManager.TogglePause()
			m.updateIcon(newState)

			// Update menu text
			if newState == state.Paused {
				m.pauseItem.SetTitle("Resume")
			} else {
				m.pauseItem.SetTitle("Pause")
			}
		}
	}
}

func (m *Manager) handleLanguageClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.languageItem.ClickedCh:
			m.configMu.Lock()
			if m.config.Deepgram.Language == "en" {
				m.config.Deepgram.Language = "de"
			} else {
				m.config.Deepgram.Language = "en"
			}

			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}

			m.updateLanguageMenu()
			fmt.Printf("[Tray] Language switched to: %s\n", m.config.Deepgram.Language)
			m.configMu.Unlock()
		}
	}
}

func (m *Manager) updateLanguageMenu() {
	m.configMu.RLock()
	defer m.configMu.RUnlock()

	if m.config.Deepgram.Language == "en" {
		m.languageItem.SetTitle("Language: English")
	} else {
		m.languageItem.SetTitle("Language: German")
	}
}

func (m *Manager) handleAutostartClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.autostartItem.ClickedCh:
			enabled := m.autostartItem.Checked()
			newState := !enabled

			if err := setAutostart(newState); err != nil {
				fmt.Printf("[Tray] Failed to set autostart: %v\n", err)
				continue
			}

			if newState {
				m.autostartItem.Check()
			} else {
				m.autostartItem.Uncheck()
			}

			fmt.Printf("[Tray] Autostart %s\n", map[bool]string{true: "enabled", false: "disabled"}[newState])
		}
	}
}

func (m *Manager) handleSettingsClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.settingsItem.ClickedCh:
			if err := config.OpenInEditor(); err != nil {
				fmt.Printf("[Tray] Failed to open settings: %v\n", err)
			}
		}
	}
}

func (m *Manager) handleLogClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.logItem.ClickedCh:
			if err := openLogFile(); err != nil {
				fmt.Printf("[Tray] Failed to open log: %v\n", err)
			}
		}
	}
}

func openLogFile() error {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return fmt.Errorf("APPDATA not set")
	}
	logPath := fmt.Sprintf("%s\\dictation\\debug.log", appData)

	proc := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	_, err := os.StartProcess("C:\\Windows\\System32\\cmd.exe", []string{"/C", "start", "", logPath}, proc)
	return err
}

func (m *Manager) handleDashboardClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.dashboardItem.ClickedCh:
			if err := openDashboard(); err != nil {
				fmt.Printf("[Tray] Failed to open dashboard: %v\n", err)
			}
		}
	}
}

func openDashboard() error {
	dashboardURL := "https://console.deepgram.com/"

	proc := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	_, err := os.StartProcess("C:\\Windows\\System32\\cmd.exe",
		[]string{"/C", "start", "", dashboardURL}, proc)
	return err
}

func (m *Manager) handleRestartClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.restartItem.ClickedCh:
			fmt.Println("[Tray] Restarting application...")
			if err := restartApplication(); err != nil {
				fmt.Printf("[Tray] Failed to restart: %v\n", err)
			}
		}
	}
}

func restartApplication() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Start new instance
	proc := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	_, err = os.StartProcess(exePath, []string{}, proc)
	if err != nil {
		return fmt.Errorf("failed to start new instance: %w", err)
	}

	// Exit current instance after a small delay to let the new one start
	go func() {
		time.Sleep(500 * time.Millisecond)
		systray.Quit()
		os.Exit(0)
	}()

	return nil
}

func (m *Manager) handleExitClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.exitItem.ClickedCh:
			fmt.Println("[Tray] Exiting...")
			systray.Quit()
			os.Exit(0)
		}
	}
}

func (m *Manager) handleIconUpdates() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastState state.AppState = state.Active

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			currentState := m.stateManager.Get()
			if currentState != lastState {
				m.updateIcon(currentState)

				// Update pause menu text
				if currentState == state.Paused {
					m.pauseItem.SetTitle("Resume")
				} else {
					m.pauseItem.SetTitle("Pause")
				}

				lastState = currentState
			}
		}
	}
}

func (m *Manager) updateIcon(appState state.AppState) {
	iconData := getIconData(appState.IconColor())
	systray.SetIcon(iconData)
}

// Removed - using icons.go instead

func isAutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	_, _, err = k.GetStringValue("DictationApp")
	return err == nil
}

func setAutostart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open registry key: %w", err)
	}
	defer k.Close()

	if enable {
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}

		if err := k.SetStringValue("DictationApp", exePath); err != nil {
			return fmt.Errorf("failed to set registry value: %w", err)
		}
	} else {
		if err := k.DeleteValue("DictationApp"); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("failed to delete registry value: %w", err)
		}
	}

	return nil
}

