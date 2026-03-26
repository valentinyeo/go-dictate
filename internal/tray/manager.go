package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/getlantern/systray"
	"github.com/valen/go-dictate/internal/audio"
	"github.com/valen/go-dictate/internal/config"
	"github.com/valen/go-dictate/internal/logger"
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
	soundSettingsItem *systray.MenuItem
	resetAudioItem    *systray.MenuItem
	restartItem       *systray.MenuItem
	exitItem          *systray.MenuItem

	// Feature toggles
	duckingItem      *systray.MenuItem
	autoPauseItem    *systray.MenuItem
	punctuateItem    *systray.MenuItem
	fillerWordsItem  *systray.MenuItem
	zeroLatencyItem  *systray.MenuItem

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
	micParent    *systray.MenuItem
	micItems     []*systray.MenuItem
	MicChangedCh chan string // sends device name when user selects a new mic

	// Signals VAD to reset (close active Deepgram connection and pick up new settings)
	SettingsChangedCh chan struct{}

	readyCh chan struct{} // closed when tray menu is fully constructed

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
		readyCh:           make(chan struct{}),
	}
}

func (m *Manager) Run() {
	systray.Run(m.onReady, m.onExit)
}

func (m *Manager) onReady() {
	// Set initial icon
	m.updateIcon(m.stateManager.Get())
	systray.SetTooltip("Dictation App v1.8.83")

	// Create menu items - clicking version opens changelog
	m.versionItem = systray.AddMenuItem("v1.8.83 - Ultra Fast", "Click to view changelog")
	summaryItem := systray.AddMenuItem("  Fix media auto-pause broken when ducking enabled", "")
	summaryItem.Disable()
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

	// Microphone submenu - show current mic in label
	micLabel := "Microphone: System Default"
	if m.selectedMic != "" {
		micLabel = "Microphone: " + m.selectedMic
	}
	m.micParent = systray.AddMenuItem(micLabel, "Select input device")
	defaultChecked := m.selectedMic == ""
	defaultItem := m.micParent.AddSubMenuItemCheckbox("System Default", "Use system default microphone", defaultChecked)
	m.micItems = append(m.micItems, defaultItem)
	for _, name := range m.micNames {
		checked := name == m.selectedMic
		item := m.micParent.AddSubMenuItemCheckbox(name, name, checked)
		m.micItems = append(m.micItems, item)
	}

	m.autostartItem = systray.AddMenuItemCheckbox("Start on Boot", "Start application on boot", isAutostartEnabled())
	systray.AddSeparator()

	// Feature toggles
	m.configMu.RLock()
	m.zeroLatencyItem = systray.AddMenuItemCheckbox("Ultra Fast Mode", "Persistent connection + 700ms silence (zero-latency)", m.config.Audio.ZeroLatency)
	m.duckingItem = systray.AddMenuItemCheckbox("Audio Ducking", "Lower other apps' volume while speaking", m.config.Audio.DuckEnabled)
	m.autoPauseItem = systray.AddMenuItemCheckbox("Auto-pause in Meetings", "Pause dictation during calls", m.config.Audio.AutoPauseOnMedia)
	m.punctuateItem = systray.AddMenuItemCheckbox("Punctuation", "Auto punctuation and capitalization", m.config.Deepgram.Punctuate)
	m.fillerWordsItem = systray.AddMenuItemCheckbox("Remove Filler Words", "Remove um, uh, like, etc.", m.config.Deepgram.FillerWords)
	m.configMu.RUnlock()

	systray.AddSeparator()
	m.settingsItem = systray.AddMenuItem("Open Config File", "Open configuration file")
	m.logItem = systray.AddMenuItem("Open Debug Log", "Open debug log file")
	m.dashboardItem = systray.AddMenuItem("Open Deepgram Dashboard", "Open Deepgram console")
	m.soundSettingsItem = systray.AddMenuItem("Open Sound Settings", "Open Windows sound input settings")
	systray.AddSeparator()
	m.pauseItem = systray.AddMenuItem("⏸ Pause / Resume", "Ctrl+Alt+X: Pause or resume dictation")
	m.resetAudioItem = systray.AddMenuItem("🔊 Reset Audio", "Restart app to reset audio capture")
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
	go m.handleSoundSettingsClick()
	go m.handleResetAudioClick()
	go m.handleRestartClick()
	go m.handleExitClick()
	go m.handleIconUpdates()

	// Subclass systray window for left-click toggle + right-click menu fix
	m.EnableLeftClick()

	// Signal that tray menu is fully constructed
	close(m.readyCh)
}

func (m *Manager) ReadyCh() <-chan struct{} {
	return m.readyCh
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

	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		CmdLine:    fmt.Sprintf(`cmd /c start "" "%s"`, changelogPath),
	}
	return cmd.Start()
}

func (m *Manager) notifySettingsChanged() {
	select {
	case m.SettingsChangedCh <- struct{}{}:
	default:
	}
}

// CycleMic advances to the next microphone in the list and returns the display name.
// Safe to call from any goroutine (e.g. a hotkey callback).
func (m *Manager) CycleMic() string {
	// Wait for tray to be ready (menu items created)
	<-m.readyCh

	if len(m.micItems) <= 1 {
		return "System Default"
	}

	// Find current index: 0 = System Default, 1..N = named devices
	currentIdx := 0
	for i, name := range m.micNames {
		if name == m.selectedMic {
			currentIdx = i + 1
			break
		}
	}

	// Advance to next (wrap around)
	nextIdx := (currentIdx + 1) % len(m.micItems)

	var deviceName string
	if nextIdx > 0 {
		deviceName = m.micNames[nextIdx-1]
	}

	// Update checkmarks
	for j, item := range m.micItems {
		if j == nextIdx {
			item.Check()
		} else {
			item.Uncheck()
		}
	}

	// Save to config
	m.selectedMic = deviceName
	m.configMu.Lock()
	m.config.Audio.DeviceName = deviceName
	if err := m.config.Save(); err != nil {
		fmt.Printf("[Tray] Failed to save config: %v\n", err)
	}
	m.configMu.Unlock()

	// Sync to Windows default
	if deviceName != "" {
		if err := audio.SetWindowsDefaultCaptureDevice(deviceName); err != nil {
			logger.Info("[MicSync] Failed to set Windows default: %v", err)
		}
	}

	// Notify main to restart audio capture
	select {
	case m.MicChangedCh <- deviceName:
	default:
	}

	displayName := deviceName
	if displayName == "" {
		displayName = "System Default"
	}
	m.micParent.SetTitle("Microphone: " + displayName)
	fmt.Printf("[Tray] Microphone cycled to: %s\n", displayName)
	return displayName
}

// ToggleZeroLatency flips Ultra Fast mode and returns the new state.
// Safe to call from any goroutine (e.g. a hotkey callback).
func (m *Manager) ToggleZeroLatency() bool {
	<-m.readyCh

	m.configMu.Lock()
	m.config.Audio.ZeroLatency = !m.config.Audio.ZeroLatency
	enabled := m.config.Audio.ZeroLatency
	if enabled {
		m.zeroLatencyItem.Check()
	} else {
		m.zeroLatencyItem.Uncheck()
	}
	if err := m.config.Save(); err != nil {
		fmt.Printf("[Tray] Failed to save config: %v\n", err)
	}
	m.configMu.Unlock()

	m.notifySettingsChanged()
	return enabled
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

			// Sync to Windows default (only for named devices, not "System Default")
			if deviceName != "" {
				if err := audio.SetWindowsDefaultCaptureDevice(deviceName); err != nil {
					logger.Info("[MicSync] Failed to set Windows default: %v", err)
				} else {
					logger.Info("[MicSync] Windows default capture device set to: %s", deviceName)
				}
			}

			// Notify main to restart audio capture
			select {
			case m.MicChangedCh <- deviceName:
			default:
			}

			displayName := deviceName
			if displayName == "" {
				displayName = "System Default"
			}
			m.micParent.SetTitle("Microphone: " + displayName)
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
		case <-m.zeroLatencyItem.ClickedCh:
			m.configMu.Lock()
			m.config.Audio.ZeroLatency = !m.config.Audio.ZeroLatency
			if m.config.Audio.ZeroLatency {
				m.zeroLatencyItem.Check()
			} else {
				m.zeroLatencyItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Ultra Fast mode: %v\n", m.config.Audio.ZeroLatency)
			m.configMu.Unlock()
			m.notifySettingsChanged()

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

	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		CmdLine:    fmt.Sprintf(`cmd /c start "" "%s"`, logPath),
	}
	return cmd.Start()
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

	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		CmdLine:    fmt.Sprintf(`cmd /c start "" "%s"`, dashboardURL),
	}
	return cmd.Start()
}

func (m *Manager) handleSoundSettingsClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.soundSettingsItem.ClickedCh:
			cmd := exec.Command("cmd")
			cmd.SysProcAttr = &syscall.SysProcAttr{
				HideWindow: true,
				CmdLine:    `cmd /c start ms-settings:sound`,
			}
			if err := cmd.Start(); err != nil {
				fmt.Printf("[Tray] Failed to open sound settings: %v\n", err)
			}
		}
	}
}

func (m *Manager) handleResetAudioClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.resetAudioItem.ClickedCh:
			logger.Info("[Tray] Reset Audio clicked - restarting app to get fresh capture")
			if err := restartApplication(); err != nil {
				logger.Error("[Tray] Failed to restart: %v", err)
			}
		}
	}
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

	// Use cmd.exe to wait 2 seconds then launch new instance.
	// This ensures the current process exits and releases the single-instance
	// mutex before the new process starts.
	// Use CmdLine directly to avoid Go escaping quotes (which breaks cmd start).
	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		CmdLine:    fmt.Sprintf(`cmd /c ping 127.0.0.1 -n 3 >nul & start "" "%s"`, exePath),
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to schedule restart: %w", err)
	}

	// Exit immediately so mutex is released
	systray.Quit()
	os.Exit(0)
	return nil
}

func (m *Manager) handleModeClicks() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.onlineItem.ClickedCh:
			logger.Info("[Tray] User clicked Online (Deepgram)")
			m.onlineItem.Check()
			m.offlineItem.Uncheck()
			m.configMu.Lock()
			m.config.Offline.Enabled = false
			if err := m.config.Save(); err != nil {
				logger.Error("[Tray] Failed to save config: %v", err)
			}
			m.configMu.Unlock()
			logger.Info("[Tray] Switched to Online (Deepgram)")
			systray.SetTooltip("Dictation App v1.8.83 - Online (Deepgram)")
			m.notifySettingsChanged()

		case <-m.offlineItem.ClickedCh:
			logger.Info("[Tray] User clicked Offline (Vosk)")
			m.configMu.RLock()
			lang := m.config.Deepgram.Language
			isLarge := m.config.Offline.ModelSize == "large"
			m.configMu.RUnlock()

			modelKey := vosk.ModelKeyForLanguage(lang, isLarge)
			logger.Info("[Tray] Offline model key: %s (DLL available: %v, model downloaded: %v)",
				modelKey, vosk.IsAvailable(), vosk.IsModelDownloaded(modelKey))

			if vosk.IsAvailable() && vosk.IsModelDownloaded(modelKey) {
				m.offlineItem.Check()
				m.onlineItem.Uncheck()
				m.configMu.Lock()
				m.config.Offline.Enabled = true
				m.config.Save()
				m.configMu.Unlock()
				logger.Info("[Tray] Switched to Offline (Vosk) - already ready")
				systray.SetTooltip("Dictation App v1.8.83 - Offline (Vosk)")
				m.notifySettingsChanged()
			} else {
				logger.Info("[Tray] Starting offline preparation (download DLL + model)...")
				go m.prepareOffline(modelKey)
			}
		}
	}
}

func (m *Manager) prepareOffline(modelKey string) {
	// Download DLL if needed
	if !vosk.IsAvailable() {
		logger.Info("[Offline] Downloading Vosk library...")
		m.offlineItem.SetTitle("Offline (downloading library...)")
		systray.SetTooltip("Downloading Vosk library...")
		if err := vosk.EnsureVoskLibrary(func(downloaded, total int64) {
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				msg := fmt.Sprintf("Offline (library %.0f%%)", pct)
				m.offlineItem.SetTitle(msg)
				systray.SetTooltip(fmt.Sprintf("Downloading Vosk library... %.0f%%", pct))
			}
		}); err != nil {
			logger.Error("[Offline] Failed to download vosk library: %v", err)
			m.offlineItem.SetTitle("Offline (Vosk) - download failed")
			systray.SetTooltip("Dictation App v1.8.83")
			return
		}
		logger.Info("[Offline] Vosk library downloaded successfully")
	}

	// Download model if needed
	if !vosk.IsModelDownloaded(modelKey) {
		info := vosk.Models[modelKey]
		logger.Info("[Offline] Downloading model: %s (%s)...", info.Name, info.Size)
		m.offlineItem.SetTitle(fmt.Sprintf("Offline (downloading %s...)", info.Name))
		systray.SetTooltip(fmt.Sprintf("Downloading %s model...", info.Name))
		if err := vosk.DownloadModel(modelKey, func(downloaded, total int64) {
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				m.offlineItem.SetTitle(fmt.Sprintf("Offline (model %.0f%%)", pct))
				systray.SetTooltip(fmt.Sprintf("Downloading %s... %.0f%%", info.Name, pct))
			}
		}); err != nil {
			logger.Error("[Offline] Failed to download model %s: %v", modelKey, err)
			m.offlineItem.SetTitle("Offline (Vosk) - download failed")
			systray.SetTooltip("Dictation App v1.8.83")
			return
		}
		logger.Info("[Offline] Model %s downloaded successfully", modelKey)
	}

	// All ready
	m.offlineItem.SetTitle("Offline (Vosk)")
	m.offlineItem.Check()
	m.onlineItem.Uncheck()
	m.configMu.Lock()
	m.config.Offline.Enabled = true
	if m.config.Offline.ModelSize == "" {
		m.config.Offline.ModelSize = "small"
	}
	m.config.Save()
	m.configMu.Unlock()
	logger.Info("[Offline] Offline mode ready!")
	systray.SetTooltip("Dictation App v1.8.83 - Offline (Vosk)")
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
	// Watch for Windows theme changes via RegNotifyChangeKeyValue (zero CPU)
	themeChanged := make(chan struct{}, 1)
	go watchThemeChanges(m.ctx, themeChanged)

	var lastState state.AppState = state.Active
	stateChangeCh := m.stateManager.Changed()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-themeChanged:
			m.updateIcon(m.stateManager.Get())
		case <-stateChangeCh:
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

