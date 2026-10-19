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
	"golang.org/x/sys/windows/registry"
)

type Manager struct {
	stateManager *state.Manager
	config       *config.Config
	configMu     sync.RWMutex

	appVersion        string
	appVersionName    string
	appVersionSummary string

	versionItem       *systray.MenuItem
	pauseItem         *systray.MenuItem
	languageItem      *systray.MenuItem
	autostartItem     *systray.MenuItem
	settingsItem      *systray.MenuItem
	logItem           *systray.MenuItem
	dashboardItem     *systray.MenuItem
	soundSettingsItem *systray.MenuItem
	resetAudioItem    *systray.MenuItem
	restartItem       *systray.MenuItem
	exitItem          *systray.MenuItem

	// Speech profiles
	profileParent       *systray.MenuItem
	profileTerminalItem *systray.MenuItem
	profileEmailItem    *systray.MenuItem
	profileCustomItem   *systray.MenuItem
	noiseModeParent     *systray.MenuItem
	noiseNormalItem     *systray.MenuItem
	noiseTrainItem      *systray.MenuItem

	// Capture mode choices in the top-level right-click menu.
	pushToTalkItem *systray.MenuItem
	alwaysOnItem   *systray.MenuItem

	// Feature toggles
	speechOptionsParent   *systray.MenuItem
	duckingItem           *systray.MenuItem
	ignoreSystemAudioItem *systray.MenuItem
	autoPauseItem         *systray.MenuItem
	punctuateItem         *systray.MenuItem
	smartFormatItem       *systray.MenuItem
	fillerWordsItem       *systray.MenuItem
	holdbackItem          *systray.MenuItem
	zeroLatencyItem       *systray.MenuItem
	saverModeItem         *systray.MenuItem
	failedAudioItem       *systray.MenuItem
	keytermsItem          *systray.MenuItem

	// Deepgram settings
	deepgramModelParent *systray.MenuItem

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

func NewManager(stateManager *state.Manager, cfg *config.Config, micDevices []string, selectedMic, appVersion, appVersionName, appVersionSummary string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		stateManager:      stateManager,
		config:            cfg,
		ctx:               ctx,
		cancel:            cancel,
		appVersion:        appVersion,
		appVersionName:    appVersionName,
		appVersionSummary: appVersionSummary,
		micNames:          micDevices,
		selectedMic:       selectedMic,
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
	systray.SetTooltip(m.baseTooltip())

	// Create menu items - clicking version opens changelog
	m.versionItem = systray.AddMenuItem(fmt.Sprintf("v%s - %s", m.appVersion, m.appVersionName), "Click to view changelog")
	summaryItem := systray.AddMenuItem("  "+m.appVersionSummary, "")
	summaryItem.Disable()
	systray.AddSeparator()

	// Profile and Deepgram settings
	m.configMu.RLock()
	pushToTalk := m.config.Audio.PushToTalk
	profile := m.config.DisplayProfile()
	punctuate := m.config.Deepgram.Punctuate
	smartFormat := m.config.Deepgram.SmartFormat
	fillerWords := m.config.Deepgram.FillerWords
	holdback := m.config.Deepgram.Holdback
	noiseMode := config.NormalizeNoiseMode(m.config.VAD.NoiseMode)
	m.configMu.RUnlock()

	m.pushToTalkItem = systray.AddMenuItemCheckbox("Push to talk (hold Ctrl+Space)", "Open the microphone only while holding Ctrl+Space", pushToTalk)
	m.alwaysOnItem = systray.AddMenuItemCheckbox("Always on", "Keep the microphone open and detect speech automatically", !pushToTalk)
	systray.AddSeparator()

	m.profileParent = systray.AddMenuItem("Profile", "Choose a dictation preset")
	m.profileTerminalItem = m.profileParent.AddSubMenuItemCheckbox("Terminal / Agent", "Fast append-only dictation for terminals and AI agents", profile == config.ProfileTerminalAgent)
	m.profileEmailItem = m.profileParent.AddSubMenuItemCheckbox("Email / Writing", "Slower final transcripts with cleaner formatting", profile == config.ProfileEmailWriting)
	m.profileCustomItem = m.profileParent.AddSubMenuItemCheckbox("Custom", "Current settings do not match a preset", profile == config.ProfileCustom)
	m.profileCustomItem.Disable()

	m.deepgramModelParent = systray.AddMenuItem("Deepgram: Nova-3", "Cloud speech recognition")
	m.punctuateItem = m.deepgramModelParent.AddSubMenuItemCheckbox("Punctuation", "Deepgram only: auto punctuation and capitalization", punctuate)
	m.smartFormatItem = m.deepgramModelParent.AddSubMenuItemCheckbox("Smart Format", "Deepgram only: format numbers, dates, currency, etc.", smartFormat)
	m.fillerWordsItem = m.deepgramModelParent.AddSubMenuItemCheckbox("Include Filler Words", "Deepgram only: include um, uh, etc. instead of stripping common fillers", fillerWords)
	m.holdbackItem = m.deepgramModelParent.AddSubMenuItemCheckbox("Hold Back Last Word (Experimental)", "Deepgram Ultra Fast only: delay the newest interim word", holdback)
	m.keytermsItem = m.deepgramModelParent.AddSubMenuItem("Edit Keyterms", "Deepgram only: open config.toml to edit keyterms")

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

	m.noiseModeParent = systray.AddMenuItem("Noise Mode: "+noiseModeDisplayName(noiseMode), "Tune speech detection for the current environment")
	m.noiseNormalItem = m.noiseModeParent.AddSubMenuItemCheckbox("Normal", "Use the configured VAD settings without audio filtering", noiseMode == config.NoiseModeNormal)
	m.noiseTrainItem = m.noiseModeParent.AddSubMenuItemCheckbox("Train / Noisy Travel", "Filter low-frequency rumble and require sustained speech", noiseMode == config.NoiseModeTrain)

	m.autostartItem = systray.AddMenuItemCheckbox("Start on Boot", "Start application on boot", isAutostartEnabled())
	systray.AddSeparator()

	// General options that are not tied to a specific STT provider.
	m.configMu.RLock()
	m.speechOptionsParent = systray.AddMenuItem("General Options", "Dictation behavior outside the STT provider")
	m.zeroLatencyItem = m.speechOptionsParent.AddSubMenuItemCheckbox("Ultra Fast / Fast Finalize", "Keep a persistent Deepgram connection and type interim results", m.config.Audio.ZeroLatency)
	m.saverModeItem = m.speechOptionsParent.AddSubMenuItemCheckbox("Battery & Budget Saver", "Close each request quickly, shorten trailing audio, and skip paid keyterms", m.config.Audio.BatteryBudgetSaver)
	m.duckingItem = m.speechOptionsParent.AddSubMenuItemCheckbox("Audio Ducking", "Lower other apps' volume while speaking", m.config.Audio.DuckEnabled)
	m.ignoreSystemAudioItem = m.speechOptionsParent.AddSubMenuItemCheckbox("Ignore System Audio", "Reject speaker bleed so background media cannot trigger dictation", m.config.Audio.IgnoreSystemAudio)
	m.autoPauseItem = m.speechOptionsParent.AddSubMenuItemCheckbox("Auto-pause in Meetings", "Pause dictation during calls", m.config.Audio.AutoPauseOnMedia)
	m.failedAudioItem = m.speechOptionsParent.AddSubMenuItemCheckbox("Save Failed Utterance WAVs", "Privacy-sensitive: save audio only when VAD detects speech but no transcript is produced", m.config.Diagnostics.SaveFailedAudio)
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
	go m.handleProfileClicks()
	go m.handleMicClicks()
	go m.handleNoiseModeClicks()
	go m.handlePauseClick()
	go m.handleLanguageClick()
	go m.handleAutostartClick()
	go m.handleVersionClick()
	go m.handleFeatureToggles()
	go m.handleSettingsClick()
	go m.handleKeytermsClick()
	go m.handleLogClick()
	go m.handleDashboardClick()
	go m.handleSoundSettingsClick()
	go m.handleResetAudioClick()
	go m.handleRestartClick()
	go m.handleExitClick()
	go m.handleIconUpdates()

	// Subclass systray window for left-click toggle + right-click menu fix
	m.EnableLeftClick()

	// Synchronize checkboxes and effective option availability.
	m.syncMenuChecks()

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

// CREATE_NO_WINDOW: never allocate a console for the child process.
// HideWindow alone is ignored when Windows Terminal is the default console
// host (Windows 11), which pops a visible terminal window per spawn.
const createNoWindow = 0x08000000

func openChangelog() error {
	exeDir := getExeDir()
	changelogPath := fmt.Sprintf("%s\\CHANGELOG.md", exeDir)

	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       fmt.Sprintf(`cmd /c start "" "%s"`, changelogPath),
	}
	return cmd.Start()
}

func (m *Manager) notifySettingsChanged() {
	select {
	case m.SettingsChangedCh <- struct{}{}:
	default:
	}
}

func setMenuItemChecked(item *systray.MenuItem, checked bool) {
	if item == nil {
		return
	}
	if checked {
		item.Check()
	} else {
		item.Uncheck()
	}
}

func profileDisplayName(profile string) string {
	switch config.NormalizeProfile(profile) {
	case config.ProfileTerminalAgent:
		return "Terminal / Agent"
	case config.ProfileEmailWriting:
		return "Email / Writing"
	default:
		return "Custom"
	}
}

func noiseModeDisplayName(mode string) string {
	if config.NormalizeNoiseMode(mode) == config.NoiseModeTrain {
		return "Train"
	}
	return "Normal"
}

func (m *Manager) baseTooltip() string {
	if m.appVersion == "" {
		return "Dictation App"
	}
	return fmt.Sprintf("Dictation App v%s", m.appVersion)
}

func (m *Manager) syncMenuChecks() {
	m.configMu.RLock()
	profile := m.config.DisplayProfile()
	zeroLatency := m.config.Audio.ZeroLatency
	saverMode := m.config.Audio.BatteryBudgetSaver
	pushToTalk := m.config.Audio.PushToTalk
	ducking := m.config.Audio.DuckEnabled
	ignoreSystemAudio := m.config.Audio.IgnoreSystemAudio
	autoPause := m.config.Audio.AutoPauseOnMedia
	saveFailedAudio := m.config.Diagnostics.SaveFailedAudio
	punctuate := m.config.Deepgram.Punctuate
	smartFormat := m.config.Deepgram.SmartFormat
	fillerWords := m.config.Deepgram.FillerWords
	holdback := m.config.Deepgram.Holdback
	noiseMode := config.NormalizeNoiseMode(m.config.VAD.NoiseMode)
	m.configMu.RUnlock()

	setMenuItemChecked(m.profileTerminalItem, profile == config.ProfileTerminalAgent)
	setMenuItemChecked(m.profileEmailItem, profile == config.ProfileEmailWriting)
	setMenuItemChecked(m.profileCustomItem, profile == config.ProfileCustom)
	setMenuItemChecked(m.noiseNormalItem, noiseMode == config.NoiseModeNormal)
	setMenuItemChecked(m.noiseTrainItem, noiseMode == config.NoiseModeTrain)
	if m.noiseModeParent != nil {
		m.noiseModeParent.SetTitle("Noise Mode: " + noiseModeDisplayName(noiseMode))
	}
	setMenuItemChecked(m.zeroLatencyItem, zeroLatency)
	setMenuItemChecked(m.saverModeItem, saverMode)
	setMenuItemChecked(m.pushToTalkItem, pushToTalk)
	setMenuItemChecked(m.alwaysOnItem, !pushToTalk)
	if m.zeroLatencyItem != nil {
		if saverMode || pushToTalk {
			m.zeroLatencyItem.Disable()
		} else {
			m.zeroLatencyItem.Enable()
		}
	}
	setMenuItemChecked(m.duckingItem, ducking)
	setMenuItemChecked(m.ignoreSystemAudioItem, ignoreSystemAudio)
	setMenuItemChecked(m.autoPauseItem, autoPause)
	setMenuItemChecked(m.failedAudioItem, saveFailedAudio)
	setMenuItemChecked(m.punctuateItem, punctuate)
	setMenuItemChecked(m.smartFormatItem, smartFormat)
	setMenuItemChecked(m.fillerWordsItem, fillerWords)
	setMenuItemChecked(m.holdbackItem, holdback)
	systray.SetTooltip(m.baseTooltip() + " - " + m.stateManager.Get().String())
}

func (m *Manager) markProfileCustomLocked() {
	m.config.Profile = config.ProfileCustom
}

func (m *Manager) applySpeechProfile(profile string) {
	normalized := config.NormalizeProfile(profile)
	m.configMu.Lock()
	m.config.ApplyProfile(normalized)
	if err := m.config.Save(); err != nil {
		fmt.Printf("[Tray] Failed to save config: %v\n", err)
	}
	m.configMu.Unlock()

	m.syncMenuChecks()
	logger.Info("[Tray] Profile: %s", profileDisplayName(normalized))
	fmt.Printf("[Tray] Profile: %s\n", profileDisplayName(normalized))
	m.notifySettingsChanged()
}

func (m *Manager) applyNoiseMode(mode string) {
	normalized := config.NormalizeNoiseMode(mode)
	m.configMu.Lock()
	m.config.VAD.NoiseMode = normalized
	if err := m.config.Save(); err != nil {
		fmt.Printf("[Tray] Failed to save config: %v\n", err)
	}
	m.configMu.Unlock()

	m.syncMenuChecks()
	logger.Info("[Tray] Noise mode: %s", noiseModeDisplayName(normalized))
	fmt.Printf("[Tray] Noise mode: %s\n", noiseModeDisplayName(normalized))
	m.notifySettingsChanged()
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
	m.markProfileCustomLocked()
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

	m.syncMenuChecks()
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

// Selecting the checked mode is a no-op, so it cannot interrupt a recording.
func (m *Manager) setPushToTalk(enabled bool) {
	m.configMu.Lock()
	if m.config.Audio.PushToTalk == enabled {
		m.configMu.Unlock()
		return
	}
	m.config.Audio.PushToTalk = enabled
	if err := m.config.Save(); err != nil {
		logger.Error("[Tray] Failed to save capture mode: %v", err)
	}
	m.configMu.Unlock()
	m.stateManager.SetPushToTalk(enabled)
	m.syncMenuChecks()
	m.notifySettingsChanged()
	logger.Info("[Tray] Capture mode changed: push_to_talk=%v", enabled)
}

func (m *Manager) handleFeatureToggles() {
	for {
		select {
		case <-m.ctx.Done():
			return

		case <-m.pushToTalkItem.ClickedCh:
			m.setPushToTalk(true)

		case <-m.alwaysOnItem.ClickedCh:
			m.setPushToTalk(false)

		case <-m.zeroLatencyItem.ClickedCh:
			m.configMu.Lock()
			m.config.Audio.ZeroLatency = !m.config.Audio.ZeroLatency
			m.markProfileCustomLocked()
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
			m.syncMenuChecks()
			m.notifySettingsChanged()

		case <-m.saverModeItem.ClickedCh:
			m.configMu.Lock()
			m.config.Audio.BatteryBudgetSaver = !m.config.Audio.BatteryBudgetSaver
			enabled := m.config.Audio.BatteryBudgetSaver
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			logger.Info("[Tray] Battery & Budget Saver: %v", enabled)
			m.configMu.Unlock()
			m.syncMenuChecks()
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

		case <-m.ignoreSystemAudioItem.ClickedCh:
			m.configMu.Lock()
			m.config.Audio.IgnoreSystemAudio = !m.config.Audio.IgnoreSystemAudio
			if m.config.Audio.IgnoreSystemAudio {
				m.ignoreSystemAudioItem.Check()
			} else {
				m.ignoreSystemAudioItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Ignore system audio: %v\n", m.config.Audio.IgnoreSystemAudio)
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
			m.syncMenuChecks()

		case <-m.failedAudioItem.ClickedCh:
			m.configMu.Lock()
			m.config.Diagnostics.SaveFailedAudio = !m.config.Diagnostics.SaveFailedAudio
			if m.config.Diagnostics.SaveFailedAudio {
				m.failedAudioItem.Check()
			} else {
				m.failedAudioItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Save failed utterance WAVs: %v\n", m.config.Diagnostics.SaveFailedAudio)
			m.configMu.Unlock()
			m.syncMenuChecks()

		case <-m.punctuateItem.ClickedCh:
			m.configMu.Lock()
			m.config.Deepgram.Punctuate = !m.config.Deepgram.Punctuate
			m.markProfileCustomLocked()
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
			m.syncMenuChecks()
			m.notifySettingsChanged()

		case <-m.smartFormatItem.ClickedCh:
			m.configMu.Lock()
			m.config.Deepgram.SmartFormat = !m.config.Deepgram.SmartFormat
			m.markProfileCustomLocked()
			if m.config.Deepgram.SmartFormat {
				m.smartFormatItem.Check()
			} else {
				m.smartFormatItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Smart format: %v\n", m.config.Deepgram.SmartFormat)
			m.configMu.Unlock()
			m.syncMenuChecks()
			m.notifySettingsChanged()

		case <-m.fillerWordsItem.ClickedCh:
			m.configMu.Lock()
			m.config.Deepgram.FillerWords = !m.config.Deepgram.FillerWords
			m.markProfileCustomLocked()
			if m.config.Deepgram.FillerWords {
				m.fillerWordsItem.Check()
			} else {
				m.fillerWordsItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Include filler words: %v\n", m.config.Deepgram.FillerWords)
			m.configMu.Unlock()
			m.syncMenuChecks()
			m.notifySettingsChanged()

		case <-m.holdbackItem.ClickedCh:
			m.configMu.Lock()
			m.config.Deepgram.Holdback = !m.config.Deepgram.Holdback
			m.markProfileCustomLocked()
			if m.config.Deepgram.Holdback {
				m.holdbackItem.Check()
			} else {
				m.holdbackItem.Uncheck()
			}
			if err := m.config.Save(); err != nil {
				fmt.Printf("[Tray] Failed to save config: %v\n", err)
			}
			fmt.Printf("[Tray] Hold back last word: %v\n", m.config.Deepgram.Holdback)
			m.configMu.Unlock()
			m.syncMenuChecks()
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

func (m *Manager) handleKeytermsClick() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.keytermsItem.ClickedCh:
			if err := config.OpenInEditor(); err != nil {
				fmt.Printf("[Tray] Failed to open config for keyterms: %v\n", err)
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
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       fmt.Sprintf(`cmd /c start "" "%s"`, logPath),
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
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       fmt.Sprintf(`cmd /c start "" "%s"`, dashboardURL),
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
				HideWindow:    true,
				CreationFlags: createNoWindow,
				CmdLine:       `cmd /c start ms-settings:sound`,
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
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       fmt.Sprintf(`cmd /c ping 127.0.0.1 -n 3 >nul & start "" "%s"`, exePath),
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to schedule restart: %w", err)
	}

	// Exit immediately so mutex is released
	systray.Quit()
	os.Exit(0)
	return nil
}

func (m *Manager) handleProfileClicks() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.profileTerminalItem.ClickedCh:
			m.applySpeechProfile(config.ProfileTerminalAgent)
		case <-m.profileEmailItem.ClickedCh:
			m.applySpeechProfile(config.ProfileEmailWriting)
		}
	}
}

func (m *Manager) handleNoiseModeClicks() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.noiseNormalItem.ClickedCh:
			m.applyNoiseMode(config.NoiseModeNormal)
		case <-m.noiseTrainItem.ClickedCh:
			m.applyNoiseMode(config.NoiseModeTrain)
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
	systray.SetTooltip(m.baseTooltip() + " - " + appState.String())
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

// ConfigSnapshot gives a recording a stable copy of the selected settings.
func (m *Manager) ConfigSnapshot() config.Config {
	m.configMu.RLock()
	defer m.configMu.RUnlock()
	snapshot := *m.config
	snapshot.Deepgram.Keyterms = append([]string(nil), m.config.Deepgram.Keyterms...)
	return snapshot
}
