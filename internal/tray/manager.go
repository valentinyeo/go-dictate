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
	"github.com/valen/go-dictate/internal/vosk"
	"golang.org/x/sys/windows/registry"
)

type Manager struct {
	stateManager *state.Manager
	config       *config.Config
	configMu     sync.RWMutex

	versionItem   *systray.MenuItem
	pauseItem     *systray.MenuItem
	languageItem  *systray.MenuItem
	autostartItem *systray.MenuItem
	settingsItem  *systray.MenuItem
	logItem       *systray.MenuItem
	dashboardItem *systray.MenuItem
	restartItem   *systray.MenuItem
	exitItem      *systray.MenuItem

	// Feature toggles
	duckingItem     *systray.MenuItem
	autoPauseItem   *systray.MenuItem
	punctuateItem   *systray.MenuItem
	fillerWordsItem *systray.MenuItem

	// STT mode selection
	onlineItem  *systray.MenuItem
	offlineItem *systray.MenuItem

	// Offline model selection
	modelParent    *systray.MenuItem
	modelSmallItem *systray.MenuItem
	modelLargeItem *systray.MenuItem

	// Microphone selection
	micNames     []string // device names from enumeration
	selectedMic  string   // currently selected device name ("" = system default)
	micItems     []*systray.MenuItem
	MicChangedCh chan string // sends device name when user selects a new mic

	// Signals VAD to reset (close active Deepgram connection and pick up new settings)
	SettingsChangedCh chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
}

func NewManager(stateManager *state.Manager, cfg *config.Config, micDevices []string, selectedMic string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		stateManager: stateManager,
		config:       cfg,
		ctx:          ctx,
		cancel:       cancel,
		micNames:     micDevices,
		selectedMic:  selectedMic,
		MicChangedCh:      make(chan string, 1),
		SettingsChangedCh: make(chan struct{}, 1),
	}
}

func (m *Manager) Run() {
	systray.Run(m.onReady, m.onExit)
}

func (m *Manager) onReady() {
	// Set initial icon
	m.updateIcon(m.stateManager.Get())
	systray.SetTooltip("Dictation App v1.8.0")

	// Create menu items - clicking version opens changelog
	m.versionItem = systray.AddMenuItem("v1.8.0 - Offline Mode", "Click to view changelog")
	systray.AddSeparator()

	// STT mode selection
	m.configMu.RLock()
	offlineEnabled := m.config.Offline.Enabled
	isLargeModel := m.config.Offline.ModelSize == "large"
	m.configMu.RUnlock()

	m.onlineItem = systray.AddMenuItemCheckbox("Online (Deepgram)", "Use Deepgram cloud API", !offlineEnabled)
	m.offlineItem = systray.AddMenuItemCheckbox("Offline (Vosk)", "Use local Vosk model (no internet needed)", offlineEnabled)
	m.modelParent = systray.AddMenuItem("Offline Model", "Select offline model size")
	m.modelSmallItem = m.modelParent.AddSubMenuItemCheckbox("Small (~50 MB)", "Fast, lower accuracy", !isLargeModel)
	m.modelLargeItem = m.modelParent.AddSubMenuItemCheckbox("Large (~1.8 GB)", "Slower, higher accuracy", isLargeModel)

	systray.AddSeparator()
	m.languageItem = systray.AddMenuItem("Language: English", "Ctrl+Alt+L: Toggle language (EN ↔ DE)")

	// Microphone submenu
	micParent := systray.AddMenuItem("Microphone", "Select input device")
	defaultChecked := m.selectedMic == ""
	defaultItem := micParent.AddSubMenuItemCheckbox("System Default", "Use system default microphone", defaultChecked)
	m.micItems = append(m.micItems, defaultItem)
	for _, name := range m.micNames {
		checked := name == m.selectedMic
		item := micParent.AddSubMenuItemCheckbox(name, name, checked)
		m.micItems = append(m.micItems, item)
	}

	m.autostartItem = systray.AddMenuItemCheckbox("Start on Boot", "Start application on boot", isAutostartEnabled())
	systray.AddSeparator()

	// Feature toggles
	m.configMu.RLock()
	m.duckingItem = systray.AddMenuItemCheckbox("Audio Ducking", "Lower other apps' volume while speaking", m.config.Audio.DuckEnabled)
	m.autoPauseItem = systray.AddMenuItemCheckbox("Auto-pause in Meetings", "Pause dictation during calls", m.config.Audio.AutoPauseOnMedia)
	m.punctuateItem = systray.AddMenuItemCheckbox("Punctuation", "Auto punctuation and capitalization", m.config.Deepgram.Punctuate)
	m.fillerWordsItem = systray.AddMenuItemCheckbox("Remove Filler Words", "Remove um, uh, like, etc.", m.config.Deepgram.FillerWords)
	m.configMu.RUnlock()

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
	go m.handleModeClicks()
	go m.handleModelClicks()
	go m.handleMicClicks()
	go m.handlePauseClick()
	go m.handleLanguageClick()
	go m.handleAutostartClick()
	go m.handleVersionClick()
	go m.handleFeatureToggles()
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

func (m *Manager) handleVersionClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.versionItem.ClickedCh:
			if err := openChangelog(); err != nil {
				fmt.Printf("[Tray] Failed to open changelog: %v\n", err)
			}
		}
	}
}

func openChangelog() error {
	exeDir := getExeDir()
	changelogPath := fmt.Sprintf("%s\\CHANGELOG.md", exeDir)

	proc := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	_, err := os.StartProcess("C:\\Windows\\System32\\cmd.exe", []string{"/C", "start", "", changelogPath}, proc)
	return err
}

func (m *Manager) notifySettingsChanged() {
	select {
	case m.SettingsChangedCh <- struct{}{}:
	default:
	}
}

func (m *Manager) handleMicClicks() {
	// Launch a goroutine per mic item that forwards clicks to a single channel
	clickCh := make(chan int, 1)
	for i, item := range m.micItems {
		go func(idx int, menuItem *systray.MenuItem) {
			for {
				select {
				case <-m.ctx.Done():
					return
				case <-menuItem.ClickedCh:
					select {
					case clickCh <- idx:
					default:
					}
				}
			}
		}(i, item)
	}

	for {
		select {
		case <-m.ctx.Done():
			return
		case idx := <-clickCh:
			// Determine device name (index 0 = System Default = "")
			var deviceName string
			if idx > 0 {
				deviceName = m.micNames[idx-1]
			}

			// Update checkmarks (radio behavior)
			for j, item := range m.micItems {
				if j == idx {
					item.Check()
				} else {
					item.Uncheck()
				}
			}

			// Save to config
			m.configMu.Lock()
			m.config.Audio.DeviceName = deviceName
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			m.configMu.Unlock()

			// Notify main to restart audio capture
			select {
			case m.MicChangedCh <- deviceName:
			default:
			}

			displayName := deviceName
			if displayName == "" {
				displayName = "System Default"
			}
			fmt.Printf("[Tray] Microphone switched to: %s\n", displayName)
		}
	}
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
			lang := m.config.Deepgram.Language
			m.configMu.Unlock()

			// Update UI and notify outside lock (updateLanguageMenu acquires RLock)
			m.updateLanguageMenu()
			fmt.Printf("[Tray] Language switched to: %s\n", lang)
			m.notifySettingsChanged()
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

func (m *Manager) handleFeatureToggles() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.duckingItem.ClickedCh:
			m.configMu.Lock()
			m.config.Audio.DuckEnabled = !m.config.Audio.DuckEnabled
			if m.config.Audio.DuckEnabled {
				m.duckingItem.Check()
			} else {
				m.duckingItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Audio ducking: %v\n", m.config.Audio.DuckEnabled)
			m.configMu.Unlock()

		case <-m.autoPauseItem.ClickedCh:
			m.configMu.Lock()
			m.config.Audio.AutoPauseOnMedia = !m.config.Audio.AutoPauseOnMedia
			if m.config.Audio.AutoPauseOnMedia {
				m.autoPauseItem.Check()
			} else {
				m.autoPauseItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Auto-pause in meetings: %v\n", m.config.Audio.AutoPauseOnMedia)
			m.configMu.Unlock()

		case <-m.punctuateItem.ClickedCh:
			m.configMu.Lock()
			m.config.Deepgram.Punctuate = !m.config.Deepgram.Punctuate
			if m.config.Deepgram.Punctuate {
				m.punctuateItem.Check()
			} else {
				m.punctuateItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Punctuation: %v\n", m.config.Deepgram.Punctuate)
			m.configMu.Unlock()
			m.notifySettingsChanged()

		case <-m.fillerWordsItem.ClickedCh:
			m.configMu.Lock()
			m.config.Deepgram.FillerWords = !m.config.Deepgram.FillerWords
			if m.config.Deepgram.FillerWords {
				m.fillerWordsItem.Check()
			} else {
				m.fillerWordsItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Remove filler words: %v\n", m.config.Deepgram.FillerWords)
			m.configMu.Unlock()
			m.notifySettingsChanged()
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

func (m *Manager) handleModeClicks() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.onlineItem.ClickedCh:
			m.onlineItem.Check()
			m.offlineItem.Uncheck()
			m.configMu.Lock()
			m.config.Offline.Enabled = false
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save: %v\n", err)
			}
			m.configMu.Unlock()
			fmt.Println("[Tray] Switched to Online (Deepgram)")
			m.notifySettingsChanged()

		case <-m.offlineItem.ClickedCh:
			m.configMu.RLock()
			lang := m.config.Deepgram.Language
			isLarge := m.config.Offline.ModelSize == "large"
			m.configMu.RUnlock()

			modelKey := vosk.ModelKeyForLanguage(lang, isLarge)

			if vosk.IsAvailable() && vosk.IsModelDownloaded(modelKey) {
				m.offlineItem.Check()
				m.onlineItem.Uncheck()
				m.configMu.Lock()
				m.config.Offline.Enabled = true
				m.config.Save()
				m.configMu.Unlock()
				fmt.Println("[Tray] Switched to Offline (Vosk)")
				m.notifySettingsChanged()
			} else {
				go m.prepareOffline(modelKey)
			}
		}
	}
}

func (m *Manager) prepareOffline(modelKey string) {
	// Download DLL if needed
	if !vosk.IsAvailable() {
		m.offlineItem.SetTitle("Offline (downloading library...)")
		if err := vosk.EnsureVoskLibrary(func(downloaded, total int64) {
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				m.offlineItem.SetTitle(fmt.Sprintf("Offline (library %.0f%%)", pct))
			}
		}); err != nil {
			m.offlineItem.SetTitle("Offline (Vosk)")
			fmt.Printf("[Tray] Failed to download vosk library: %v\n", err)
			return
		}
	}

	// Download model if needed
	if !vosk.IsModelDownloaded(modelKey) {
		info := vosk.Models[modelKey]
		m.offlineItem.SetTitle(fmt.Sprintf("Offline (downloading %s...)", info.Name))
		if err := vosk.DownloadModel(modelKey, func(downloaded, total int64) {
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				m.offlineItem.SetTitle(fmt.Sprintf("Offline (model %.0f%%)", pct))
			}
		}); err != nil {
			m.offlineItem.SetTitle("Offline (Vosk)")
			fmt.Printf("[Tray] Failed to download model: %v\n", err)
			return
		}
	}

	// All ready
	m.offlineItem.SetTitle("Offline (Vosk)")
	m.offlineItem.Check()
	m.onlineItem.Uncheck()
	m.configMu.Lock()
	m.config.Offline.Enabled = true
	m.config.Save()
	m.configMu.Unlock()
	fmt.Println("[Tray] Offline mode ready!")
	m.notifySettingsChanged()
}

func (m *Manager) handleModelClicks() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.modelSmallItem.ClickedCh:
			m.modelSmallItem.Check()
			m.modelLargeItem.Uncheck()
			m.configMu.Lock()
			m.config.Offline.ModelSize = "small"
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save: %v\n", err)
			}
			m.configMu.Unlock()
			fmt.Println("[Tray] Offline model: Small")
			if m.config.Offline.Enabled {
				m.notifySettingsChanged()
			}

		case <-m.modelLargeItem.ClickedCh:
			m.modelLargeItem.Check()
			m.modelSmallItem.Uncheck()
			m.configMu.Lock()
			m.config.Offline.ModelSize = "large"
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save: %v\n", err)
			}
			m.configMu.Unlock()
			fmt.Println("[Tray] Offline model: Large")
			if m.config.Offline.Enabled {
				m.notifySettingsChanged()
			}
		}
	}
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
	var lastLanguage string = ""

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

			// Check if language changed (from hotkey or menu click)
			m.configMu.RLock()
			currentLanguage := m.config.Deepgram.Language
			m.configMu.RUnlock()

			if currentLanguage != lastLanguage {
				m.updateLanguageMenu()
				lastLanguage = currentLanguage
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

