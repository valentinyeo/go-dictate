# Changelog

All notable changes to the Dictation App will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.8.56] - 2026-02-26 "Meeting Override"

### Fixed
- **Auto-pause toggle now controls meeting/recording detection** - Previously, unchecking "Auto-pause in Meetings" only disabled media audio detection but meetings and screen recordings still forced a pause. Now the toggle controls all three: meetings, recordings, and media playback.

---

## [1.8.55] - 2026-02-26 "Auto Period"

### Added
- **Auto-insert missing period between utterances** - When Deepgram omits a trailing period at an utterance boundary and the next utterance starts with a capital letter, a period is automatically inserted. Fixes `"I'm not sure Anyways"` → `"I'm not sure. Anyways"`.

---

## [1.8.54] - 2026-02-25 "Aggressive Ducking"

### Changed
- **Duck per-app volume to 5%** (was 20%) - Previous duck level was imperceptible when app volume was already low (e.g. Edge at 21% ducked to 20%). Now ducks to 5% which is clearly audible.

---

## [1.8.53] - 2026-02-25 "Ducking Fix"

### Fixed
- **Fix audio ducking not working** - Both `AppVolumeDucker` (per-app) and `AudioDucker` (system master) were passing float arguments to COM `SetMasterVolume`/`SetMasterVolumeLevelScalar` via `unsafe.Pointer` (passing the address). COM expects float by value in XMM registers. Fixed by using `math.Float32bits()` to pass the float's bit pattern directly, which Go's `asmstdcall` correctly loads into XMM registers.

---

## [1.8.52] - 2026-02-24 "Audio Recovery"

### Added
- **Auto-restart Windows Audio service** - When the watchdog exhausts all 5 restart attempts (mic stuck producing zeros, common with Intel SST after Zoom/Teams calls), it now automatically restarts the Windows Audio service (Audiosrv) via elevated PowerShell. This is the nuclear option that reliably fixes the stuck driver state.
- **"Reset Audio" tray menu item** - Manual option to restart the Windows Audio service and relaunch the app. Shows UAC prompt.
- **Quick restart (Stop/Start)** - First 2 watchdog attempts now use a lightweight device Stop+Start instead of full teardown+rebuild, preserving driver state.

---

## [1.8.51] - 2026-02-24 "WebSocket Race Fix"

### Fixed
- **Fix concurrent websocket write panic in Deepgram client** - The close goroutine called `conn.Close()` outside the write mutex, racing with the send goroutine's `WriteMessage`. Now `conn.Close()` is held inside `wsMu.Lock()` and the `done` channel is closed first to signal the send goroutine to stop.

---

## [1.8.50] - 2026-02-22 "Smart Media Detection"

### Fixed
- **Muted videos no longer trigger media auto-pause** - Previously, `IsMediaPlaying()` checked whether an audio session was in "Active" state, but a muted video in Chrome still has an active audio session. Now uses `IAudioMeterInformation::GetPeakValue` to check if audio is actually flowing — muted or paused videos read as zero and are ignored.
- **Ducking and media auto-pause no longer conflict** - When audio ducking is enabled, media auto-pause is skipped. The user's intent with ducking is "lower the media volume and keep dictating", not "pause dictation entirely." Meetings and recordings still auto-pause regardless of ducking setting.

---

## [1.8.49] - 2026-02-22 "Efficiency Mode"

### Added
- **Windows Efficiency Mode (EcoQoS)** - The app now opts into Windows 11 Efficiency Mode at startup. Sets below-normal priority and enables power throttling. Windows shows the green leaf icon in Task Manager. The app uses less power when idle and yields CPU to foreground apps, while still being responsive enough for real-time dictation.

---

## [1.8.48] - 2026-02-22 "Zero CPU Pause"

### Fixed
- **Truly zero CPU when paused** - Three sources of CPU usage while paused have been eliminated:
  1. **WASAPI capture stopped** - Audio capture device is fully released when paused, letting `audiodg.exe` go idle. Watchdog, mic sync, and wake handler all skip work while paused.
  2. **Meeting/media ticker disabled** - The 1-second meeting/recording detection ticker is nil'd when paused (same pattern as audio channel), so no COM calls or process enumeration runs.
  3. **Hotkey polling replaced with blocking GetMessageW** - Replaced `golang.design/x/hotkey` library (which polled PeekMessage at 10ms per hotkey = 200 polls/sec for 2 hotkeys) with internal hotkey manager using blocking `GetMessageW`. The hotkey thread now sleeps in the kernel until a hotkey is actually pressed — zero CPU at all times.

## [1.8.47] - 2026-02-22 "Pause Stops Audio"

### Fixed
- **Stop WASAPI capture when paused so audiodg.exe goes idle** - Previously, pausing dictation only stopped processing audio chunks in the VAD manager, but the WASAPI capture device kept running. This forced `audiodg.exe` to continuously process the mic input stream even while paused, causing CPU spikes. Now pausing actually stops the capture device, letting `audiodg.exe` go fully idle. The watchdog, mic sync poller, and wake handler all skip work while paused to avoid interfering. Mic changes while paused are deferred until resume.

---

## [1.8.46] - 2026-02-21 "Media Check Fix"

### Fixed
- **Fix app freeze caused by heavy COM calls on every audio chunk** - `IsMediaPlaying()` performs expensive Windows COM session enumeration (CoInitializeEx, device/session enumeration). Previously it was called on every audio chunk (~100x/sec) in addition to the 3s ticker. When a COM call hung, it blocked the entire vadAndSTTManager goroutine, freezing state updates and making the tray unresponsive. Now media detection only runs on the 3s ticker; the audio chunk path uses a lightweight cached value instead.

---

## [1.8.45] - 2026-02-18 "Media Auto-Pause"

### Fixed
- **Auto-pause dictation when any media is playing** - YouTube, Spotify, music, videos, etc. now trigger auto-pause. Previously, the media detection (`IsMediaPlaying`) only set a visual indicator but didn't actually pause dictation, so the app would transcribe audio from speakers. Now media playback is treated the same as meetings/recordings - dictation auto-pauses and resumes when media stops. Controlled by the existing "Auto-pause in Meetings" toggle.

---

## [1.8.44] - 2026-02-18 "Mic Sync"

### Added
- **Two-way sync between app mic selection and Windows default input device**
  - **App → Windows**: When you pick a named mic in the tray menu, the Windows default input device is also changed to match (uses `IPolicyConfig::SetDefaultEndpoint`, same undocumented but stable COM interface used by EarTrumpet and SoundSwitch). Sets all three roles (Console, Multimedia, Communications).
  - **Windows → App**: When the app is on "System Default" mode, a 5-second poller detects changes to the Windows default capture device (e.g. changed via Sound Settings) and restarts audio capture to pick up the new device automatically.
  - Prevents the silent-mic problem where the app opens one mic while Windows has a different default.

### Fixed
- **Detect packaged apps (new Teams) for meeting auto-pause** - The meeting detector only checked `NonPackaged` registry keys, missing Microsoft Store apps like the new Teams (MSIX). Now also scans packaged app keys directly under `CapabilityAccessManager\ConsentStore\microphone\` (e.g. `MSTeams_8wekyb3d8bbwe`).

---

## [1.8.42] - 2026-02-18 "Windows Power Events"

### Fixed
- **Proper sleep/wake detection via Windows power notifications** - Instead of guessing sleep/wake from timer gaps (unreliable for short sleeps), now uses `RegisterPowerSettingNotification` to get actual Windows power events (`PBT_APMRESUMEAUTOMATIC` and display-on). On wake: waits 5s for the audio subsystem to settle, then force-restarts capture with a fresh context. Resets watchdog state so it gets a full 5 restart attempts.
- **Periodic retry from monitor mode** - Watchdog retries every 5 minutes from monitor mode instead of waiting indefinitely.

---

## [1.8.41] - 2026-02-18 "Sleep/Wake Recovery"

### Fixed
- **Watchdog recovers after sleep/wake** - Detects sleep/wake via time gap (>30s between ticks) and resets to retry audio capture.
- **Periodic retry from monitor mode** - Watchdog retries every 5 minutes from monitor mode instead of waiting indefinitely for natural recovery.

---

## [1.8.40] - 2026-02-18 "Independent Meeting Detection"

### Fixed
- **Meeting detection now works when mic is dead** - Previously, Zoom/Teams call detection only ran when audio chunks were flowing. If the mic was silent (watchdog monitor mode) or the audio channel was nil, meeting detection never triggered and the app wouldn't auto-pause. Now runs on an independent 3-second ticker, completely decoupled from audio capture state.

---

## [1.8.39] - 2026-02-18 "Sound Settings Shortcut"

### Added
- **Open Sound Settings** menu item in tray - opens Windows Sound Settings (`ms-settings:sound`) for quick mic troubleshooting.

---

## [1.8.38] - 2026-02-18 "Watchdog Monitor Mode"

### Fixed
- **Stop restarting after 5 failures** - Previous versions kept restarting the audio device indefinitely (84+ restarts observed), leaking memory (21MB → 39MB) and freezing the tray UI. Now: 5 quick restart attempts (first 2 with configured device, last 3 with system default), then enter "monitor mode" — just watches the RMS passively with zero resource churn. If the mic comes back naturally (wireless reconnect), it auto-recovers and logs it.

---

## [1.8.37] - 2026-02-17 "Watchdog Backoff"

### Fixed
- **Exponential backoff on restart failures** - v1.8.36's aggressive 20s restart cycle caused the watchdog to hammer audio device init/deinit every 20 seconds when no mic worked (48+ restarts observed), making the tray UI unresponsive. Now: first 3 restarts are immediate (quick recovery), then backoff kicks in: 20s, 40s, 60s... capped at 60s between attempts. App stays responsive while waiting for the mic to come back.
- **Cleaner recovery logging** - logs when audio actually recovers after failures.

---

## [1.8.36] - 2026-02-17 "Fast Wireless Mic Recovery"

### Fixed
- **Faster flat-line detection** - Reduced from 60s to 20s (4 checks instead of 12). Wireless mics like the Lark M2 drop their wireless link frequently, delivering complete digital silence (0.000000 RMS) while the USB dongle stays connected. Previous 60s timeout meant the mic sat dead for a full minute before recovery.
- **Fallback to system default mic** - After 3 consecutive restart failures with the configured device, automatically tries the system default mic. The wireless mic receiver may have fully disconnected, and a built-in mic is better than nothing.
- **Faster restart cycle** - Reduced post-stop delay from 1s to 500ms for quicker recovery.

---

## [1.8.35] - 2026-02-16 "Audio Watchdog RMS"

### Fixed
- **Detect dead-but-noisy microphone** - The audio watchdog previously only checked if the capture callback was firing. A mic could enter a degraded state (e.g., after sleep/wake) where it delivers low-level noise but no actual speech — VAD triggers on the noise, Deepgram returns empty text, and this cycles indefinitely. Now the watchdog tracks peak RMS per interval. If audio stays below RMS 0.005 for 60 consecutive seconds, the capture device is automatically restarted.
- **Periodic RMS logging** - Debug log now includes `[Watchdog] Peak RMS: X.XXXXXX` every ~30s for diagnosing audio quality issues.

---

## [1.8.34] - 2026-02-12 "Fix Open File Actions"

### Fixed
- **Fix Open Debug Log, Changelog, and Dashboard menu items** - These tray menu actions silently failed in `-H windowsgui` builds because `os.StartProcess` was called with `os.Stdin/Stdout/Stderr` which are nil when there's no console. Switched all three to `exec.Command` with `SysProcAttr{HideWindow: true, CmdLine: ...}` pattern (same as restart).

---

## [1.8.33] - 2026-02-12 "Registry Mic Detection"

### Fixed
- **Detect meetings via Windows mic privacy registry** - Previous approaches (WASAPI capture sessions, process names, window titles) all failed for Google Meet in Edge because: Chrome/Edge mic sessions don't appear in WASAPI, the Meet tab might not be the active tab (so window title doesn't match), and process name matching is fragile. New approach reads `HKCU\...\CapabilityAccessManager\ConsentStore\microphone\NonPackaged` where Windows tracks per-exe mic usage with start/stop timestamps. If `LastUsedTimeStop == 0`, the app is actively using the mic. Works for ALL meeting apps in ALL browsers regardless of tab focus.

---

## [1.8.32] - 2026-02-12 "Meeting + Menu Fix"

### Fixed
- **Detect Google Meet (and browser meetings) by window title** - WASAPI capture sessions don't expose Chrome/Edge mic usage. Added window title scanning as fallback: detects "Meet - " (Google Meet in-call), "Zoom Meeting", "| Microsoft Teams" in any visible window. Combined with mic capture check for native apps.
- **Force tray menu foreground with AttachThreadInput** - Right-click menu was blocked when Chrome/Teams had focus because `SetForegroundWindow` silently fails for background processes. Now temporarily attaches to the foreground app's input queue before showing the menu, which is the standard Windows workaround.

---

## [1.8.31] - 2026-02-12 "Meeting Detection Fix"

### Fixed
- **Meeting detection now checks microphone usage** - Previous approach checked if meeting apps (Teams, Zoom, etc.) were playing audio through speakers, which failed when: the other person was silent, the meeting was browser-based (Google Meet), or the app wasn't in the hardcoded process list. New approach checks if ANY other app has an active audio session on the capture (microphone) device. If another app is using your mic, you're in a call — works for all meeting apps automatically.

---

## [1.8.30] - 2026-02-12 "Tray Menu Fix"

### Fixed
- **Fix right-click menu not appearing** - The `getlantern/systray` library is missing the required `PostMessage(WM_NULL)` call after `TrackPopupMenu` (Microsoft KB Q135788). Without it, Windows doesn't release the foreground lock, causing the context menu to silently fail on subsequent right-clicks — especially when another app like Teams has focus. Fixed by subclassing the systray hidden window and adding the PostMessage fix.
- **Left-click tray icon now toggles pause** - Previously both left and right clicks showed the context menu (library default). Now left-click toggles pause/resume for quick one-click control without opening the menu.

---

## [1.8.29] - 2026-02-11 "Double Letter Fix"

### Fixed
- **Fix dropped duplicate letters** - Words like "tomorrow", "really", "essentially" were being typed as "tomorow", "realy", "esentialy". The keyboard simulator was only sending key-down events via `SendInput` but never key-up events. When two identical characters came back-to-back, the target application saw a held key rather than two distinct presses, so the second character was swallowed. Now sends both DOWN and UP events in a single atomic `SendInput` call for each character.

---

## [1.8.28] - 2026-02-11 "Idle CPU Fix"

### Fixed
- **Icon update polling replaced with event-driven** - The tray icon was polling every 100ms (10x/sec) just to check if the state changed. Replaced with event-driven updates via `stateManager.Changed()` channel - now zero CPU when idle. Language menu polling also removed since `handleLanguageClick()` already updates the menu directly.
- **Media detection cache 5ms -> 3s** - `IsMediaPlaying()` had a 5ms cache, meaning full COM enumeration of all audio sessions happened ~200x/sec. Media playback state doesn't change on a millisecond timescale. Increased to 3 seconds.
- **Meeting detection cache 1s -> 3s** - `IsMeetingActive()` was doing COM + process enumeration every second. Meeting state changes slowly. Increased to 3 seconds.

---

## [1.8.27] - 2026-02-11 "WebSocket Fix"

### Fixed
- **Fix concurrent websocket write panic** - The Deepgram client had two goroutines writing to the same websocket connection: the audio sender and the context-cancel closer. gorilla/websocket does not allow concurrent writes, causing `panic: concurrent write to websocket connection`. Added a write mutex to serialize all websocket writes. This was the crash caught by the new crash.log from v1.8.26.

---

## [1.8.26] - 2026-02-10 "Crash Logging"

### Fixed
- **Fix `syscall.NewCallback` resource leak** - The recording detector was creating a new Windows callback on every call to `IsRecordingActive()`. Go has a global limit of ~2000 callbacks, so after enough checks the app would crash. Fixed by creating the callback once at init time and reusing it via shared state protected by the existing mutex.
- **Crash log capture** - Panics in `-H windowsgui` builds vanish silently since there's no console. Added `RedirectStderr()` which redirects the Windows stderr handle to `%APPDATA%\dictation\crash.log`, capturing panic stack traces for diagnosis.

---

## [1.8.25] - 2026-02-10 "Recording Pause"

### Added
- **Auto-pause during screen recording** - Dictation now auto-pauses when screen recording is detected and resumes when recording stops. Detection methods:
  - **screencast2-go**: Detects `ffmpeg.exe` process (only spawned during active recording)
  - **Loom**: Detects visible Loom windows (Loom hides to tray when not recording, shows recording controls when recording)
  - Also covers OBS and any tool that spawns ffmpeg for recording

---

## [1.8.24] - 2026-02-10 "Crash Fix"

### Fixed
- **Fix crash from send-on-closed-channel** - The text bridge channel introduced in v1.8.21 was being closed while the Deepgram STT goroutine might still be writing to it. This caused a "send on closed channel" panic that silently killed the process (no console with `-H windowsgui`). Fixed by never closing the text bridge channel directly - instead, the bridge goroutine watches the STT context and exits naturally when cancelled. The channel is garbage collected when no goroutines reference it.

---

## [1.8.23] - 2026-02-10 "Audio Watchdog"

### Fixed
- **Audio watchdog** - Automatically detects when the audio capture device stops producing audio (e.g., after sleep/wake, USB disconnect) and restarts it within 10 seconds. Falls back to system default mic if the selected device fails. Previously, the app would freeze silently after sleep with no way to recover except manual restart.

---

## [1.8.22] - 2026-02-10 "Ghost Cooldown"

### Fixed
- **Ghost speech cooldown** - After detecting ghost speech (background noise with no real text), the app now enters a 30-second cooldown where VAD is suppressed. Previously, the VAD would immediately re-trigger on the next audio chunk, creating an endless 15-second loop of open→close→open connections while the icon stayed green.

---

## [1.8.21] - 2026-02-10 "Ghost Speech Fix"

### Fixed
- **Ghost speech detection** - When background audio (TV, music, podcasts) keeps the VAD triggered but Deepgram returns no actual text for 15 seconds, the STT connection is now force-closed and the icon returns to standby. Previously, background noise could keep the icon green and connection open indefinitely, occasionally typing stray words into the active window.

---

## [1.8.20] - 2026-02-09 "Restart Fix"

### Fixed
- **Restart menu item works** - Fixed "Windows cannot find '\\'" error when clicking Restart in tray. Go's `exec.Command` was escaping quotes in the `cmd /c start` command, breaking the path. Now uses `CmdLine` directly to bypass argument escaping.

---

## [1.8.19] - 2026-02-08 "Pre-Roll Buffer"

### Fixed
- **No more missing first words** - Added a 500ms pre-roll ring buffer that continuously records the last ~25 audio chunks. When VAD detects speech start, the buffer is flushed to the STT engine before the triggering chunk, so the beginning of the utterance is never lost. Previously "what do I need to do" would arrive as "I need to do" because VAD consumed the first words before firing SpeechStarted.

---

## [1.8.18] - 2026-02-08 "Mic Label"

### Changed
- **Active microphone shown in tray menu** - Menu item now reads "Microphone: Lark M2" instead of just "Microphone". See current mic at a glance without opening the submenu. Updates when you switch mics.

---

## [1.8.17] - 2026-02-08 "Audio Buffer"

### Fixed
- **No more lost words on slow internet** - Audio chunks are now buffered while waiting for Deepgram WebSocket to connect. Previously, with ~2 second connection delays on slow internet, the beginning of every utterance was lost. Now the Deepgram client dials in a background goroutine while collecting audio, then flushes the entire buffer once connected. Zero impact on fast connections.

---

## [1.8.16] - 2026-02-08 "Grey+Yellow Icon"

### Changed
- **Two-color app icon** - Start Menu icon now has light grey bubble (`#94A3B8`) with yellow sparkle stars (`#FBBF24`). Works on both dark and light themes. Separate SVG (`icons/app-icon.svg`) keeps this independent from tray icons.

---

## [1.8.15] - 2026-02-08 "Blue App Icon"

### Changed
- **Windows blue app icon** - Start Menu / exe icon now uses Windows blue `#0078D4`, visible on both light and dark themes. Tray icons remain separate (theme-adaptive standby, green active, red paused, amber media).

---

## [1.8.14] - 2026-02-08 "Black App Icon"

### Fixed
- **Start Menu icon now black speech bubble** - Regenerated embedded exe icon with black `#1a1a1a` tint instead of green. Matches the tray standby icon style.

---

## [1.8.13] - 2026-02-08 "Zero CPU Theme"

### Changed
- **Restored zero-CPU theme detection** - Uses `RegNotifyChangeKeyValue` Windows API (same as screencast2-go, AI Write, VPS Manager). Blocks on syscall until registry changes - truly zero CPU while waiting. Previous crash was likely from slow internet/Deepgram, not from theme detection.

---

## [1.8.12] - 2026-02-08 "Theme Watch"

### Fixed
- **RegNotifyChangeKeyValue crashed the app** - The synchronous blocking call caused the app to crash on theme change. Replaced with simple 30-second polling which is reliable and still negligible overhead for something that changes rarely.

---

## [1.8.11] - 2026-02-08 "Theme Watch" (crashed on theme change)

---

## [1.8.10] - 2026-02-08 "Theme Adapt"

### Fixed
- **Tray icon adapts to Windows theme changes** - Previously the icon color was set once at render time and never updated. Now re-renders the tray icon when the theme switches between light and dark mode. Standby icon automatically flips between black (light taskbar) and white (dark taskbar).

---

## [1.8.9] - 2026-02-08 "Icon Polish"

### Fixed
- **Sparkle stars clipped on right edge** - SVG viewBox was exactly 32x32 but sparkle paths extended to ~31.8px. Added 1px padding on all sides (`viewBox="-1 -1 34 34"`) so anti-aliasing renders fully.

### Changed
- **Black app icon** - Start Menu / taskbar / exe icon is now black instead of green, matching standard Windows icon style.

---

## [1.8.8] - 2026-02-08 "App Icon"

### Changed
- **Chat sparkle app icon** - Updated the embedded exe icon and Start Menu shortcut to use the green chat-sparkle icon (multi-size: 16, 32, 48, 256px)
- Updated Windows resource version info to 1.8.8

---

## [1.8.7] - 2026-02-08 "Ducking Guard"

### Fixed
- **Volume ducking ignoring disabled config** - `Restore()` was called unconditionally on every silence event regardless of `duck_enabled` setting. Now both `Duck()` and `Restore()` are gated behind the config flag. When ducking is disabled, neither call runs.

---

## [1.8.6] - 2026-02-08 "Chat X Icon"

### Changed
- **Chat bubble with X for paused/off** - Replaced slashed mic with Fluent UI chat-dismiss icon (chat bubble with X). All tray icons now use the chat bubble theme.

---

## [1.8.5] - 2026-02-08 "Simplified Icons"

### Changed
- **Simplified icon usage** - All states now use the same filled chat-sparkle icon, just tinted differently
  - Standby: black/white (theme-aware)
  - Speaking: green
  - Paused: red slashed mic (unchanged)
  - Media: amber

---

## [1.8.4] - 2026-02-08 "Chat Sparkle Icons"

### Changed
- **New chat-with-sparkles icons** - Replaced microphone icons with Fluent UI chat-with-sparkles design
  - Filled chat bubble with sparkle stars for speaking/active state (green tint)
  - Outline chat bubble with sparkle stars for standby state (theme-aware grey)
  - Kept slashed mic icon for paused/off state (red tint)
  - Better visual distinction between "AI dictation active" vs "paused"

---

## [1.8.3] - 2026-02-08 "Fluent Icons"

### Changed
- **Microsoft Fluent UI microphone icons** - Replaced custom icons with official Fluent UI System Icons
  - Filled mic for speaking (green)
  - Outline mic for standby (theme-aware grey)
  - Slashed mic for paused (red)
  - 128x128 source PNGs rendered from SVGs for maximum detail
- **Multi-size ICO** (16, 20, 24, 32, 48) for crisp display at any DPI
- **Theme-aware standby icon** - Detects Windows taskbar theme via registry. Dark icon on light taskbar, light icon on dark taskbar

## [1.8.2] - 2026-02-08 "Crisp Icons" (reverted)

## [1.8.1] - 2026-02-08 "Offline Fix"

### Fixed
- **Vosk DLL download URL** - v0.3.50 release has no assets; switched to v0.3.45 which has the actual win64 zip
- **Restart broken by single-instance mutex** - Old process held the mutex while new process tried to start, causing it to exit immediately. Now uses cmd.exe with a delay so old process exits first, releasing the mutex before the new instance launches.

## [1.8.0] - 2026-02-08 "Offline Mode"

### Added
- **Offline dictation with Vosk** - Speech-to-text without internet using local models
  - Toggle between Online (Deepgram) and Offline (Vosk) via tray menu
  - Two model sizes: Small (~50 MB, fast) and Large (~1.8 GB, more accurate)
  - Models auto-download on first use with progress shown in tray menu
  - Vosk DLL auto-downloaded on first use (~10 MB)
  - Falls back to Deepgram if model not ready
  - Language switching works in offline mode (downloads new model if needed)
- **STT Engine interface** - Abstracted speech-to-text backend behind `stt.Engine` interface
  - Both Deepgram and Vosk implement the same interface
  - Easy to add more backends in the future
- **Vosk DLL loaded at runtime** - No CGO dependency, DLL loaded via syscall
  - App works without libvosk.dll (offline mode just won't be available)
  - DLL searched next to exe, then system PATH

### Changed
- Renamed `vadAndDeepgramManager` to `vadAndSTTManager` (handles both engines)
- Config validation skips API key check when offline mode is enabled
- New `[offline]` config section: `enabled` and `model_size`

### Technical Details
- New: `internal/stt/engine.go` - STT Engine interface
- New: `internal/vosk/dll.go` - Runtime DLL loading via syscall (no CGO)
- New: `internal/vosk/engine.go` - Vosk STT engine implementation
- New: `internal/vosk/model.go` - Model registry, download manager, DLL downloader
- New: `internal/vosk/types.go` - Vosk JSON result types
- Modified: `internal/config/config.go` - Added OfflineConfig struct
- Modified: `internal/tray/manager.go` - Online/Offline radio toggle, model size submenu
- Modified: `main.go` - STT engine abstraction, vosk integration

---

## [1.7.1] - 2026-02-06 "Stability Fix"

### Fixed
- **CRITICAL: COM threading violations causing random freezes** - All Windows COM calls now lock the goroutine to its OS thread with `runtime.LockOSThread()`. Previously, Go could migrate goroutines between threads mid-COM-call, corrupting COM state and causing hangs. Affected: media detection (every 5ms), meeting detection, volume ducking.
- **Deepgram WebSocket ReadMessage blocking forever** - When context is cancelled, the websocket connection is now explicitly closed, unblocking `ReadMessage()`. Previously, leaked goroutines accumulated after each speech/silence cycle with network issues.
- **Deepgram done channel double-close panic** - Used `sync.Once` to prevent `close(done)` from being called twice when context cancellation and read error happen simultaneously.
- **Deepgram receive loop break bug** - `break` on CloseMessage only exited the `if` block, not the `for` loop. Changed to `return nil` so the function exits properly.
- **Meeting cache data race** - Added `sync.Mutex` to `meetingCache` (was unprotected, unlike `mediaCache`).
- **Kernel32 DLL created on every call** - Cached `kernel32.dll` procs at package level instead of creating new `LazyDLL` objects on every `getProcessName()` call (called hundreds of times per second).

### Improved
- **5x faster keyboard typing** - Reduced per-character delay from 10ms to 2ms, preventing transcript backpressure from blocking Deepgram receive loop.

### Technical Details
- Modified: `internal/audio/media_detector.go` - Added `runtime.LockOSThread()` to `checkMediaPlayback()`
- Modified: `internal/audio/meeting_detector.go` - Added `runtime.LockOSThread()` to `checkActiveMeetingAudio()`, mutex to cache, cached kernel32 procs
- Modified: `internal/audio/ducking.go` - Added `runtime.LockOSThread()` to `Duck()` and `Restore()`
- Modified: `internal/audio/app_volume.go` - Added `runtime.LockOSThread()` to `Duck()` and `Restore()`
- Modified: `internal/deepgram/client.go` - sync.Once for done channel, conn.Close() on context cancel, fixed break→return
- Modified: `internal/keyboard/simulator.go` - Reduced typing delay 10ms→2ms

---

## [1.7.0] - 2026-02-06 "Feature Toggles"

### Added
- **Tray menu feature toggles** - Toggle settings without editing config.toml
  - Audio Ducking (off by default - disabled until fixed)
  - Auto-pause in Meetings
  - Punctuation
  - Remove Filler Words
- **Instant settings** - Language, punctuation, and filler word changes take effect immediately
  - No longer need to restart the app after changing settings
  - Active Deepgram connection is closed and VAD resets on settings change
- **Clickable version** - Click version number in tray menu to open CHANGELOG.md

### Fixed
- **Settings required app restart** - Language switch and other Deepgram settings now take effect on the next speech burst without restarting

### Technical Details
- Added: `SettingsChangedCh` channel in tray manager to signal VAD goroutine
- Modified: `vadAndDeepgramManager` - Listens for settings changes, closes active Deepgram connection and resets VAD
- Added: `DuckEnabled` bool in config to explicitly toggle ducking on/off
- Added: `handleFeatureToggles()` in tray manager for checkbox menu items
- Modified: Hotkey language toggle also signals settings changed

---

## [1.6.0] - 2026-02-06 "Microphone Selector"

### Added
- **Microphone selector** in tray right-click menu
  - Lists all available capture devices
  - "System Default" option plus all enumerated microphones
  - Hot-switching: changes mic without restarting the app
  - Selection persisted in config.toml (`device_name` field)
- **Embedded application icon** - exe now has a green microphone icon
  - Uses go-winres for Windows resource embedding
  - Includes version info and manifest in the executable
- **Start Menu entry** - "Go Dictate" shortcut in Start Menu
- **Fixed executable name** - `go-dictate.exe` no longer includes version number
  - Start Menu shortcuts survive rebuilds

### Fixed
- **Device enumeration crash** - WASAPI needs COM initialized on the calling thread
  - Added `runtime.LockOSThread()` and `CoInitializeEx` in `ListCaptureDevices()`
- **Capture Stop() crash** - Fixed `Free()` being called before `Uninit()`
- **CGo pointer violation** - Used `DeviceID.Pointer()` instead of `unsafe.Pointer(&id)`

### Technical Details
- Added: `ListCaptureDevices()` in audio/capture.go with COM initialization
- Modified: `NewCapture()` accepts optional `*CaptureDevice` for device selection
- Added: Microphone submenu with radio-button behavior in tray manager
- Added: `MicChangedCh` channel for signaling mic changes to main.go
- Added: `winres/` directory with icon and Windows resource metadata

---

## [1.5.0] - 2026-02-05 "Per-App Volume Ducking"

### Changed
- **Per-app volume ducking** - Ducks individual app volumes instead of master volume
  - Targets: Chrome, Edge, Firefox, Brave, Opera, VLC, Screenbox, Spotify
  - Uses Windows IAudioSessionManager2 COM interface
  - More precise control than master volume ducking
- **Meeting detection** - Auto-pauses during active calls (Teams, Zoom, Slack, Discord, Skype)
  - Only pauses during active audio sessions, not just when app is running

### Technical Details
- Added: `internal/audio/app_volume.go` - Per-app volume control via IAudioSessionManager2
- Added: `internal/audio/meeting_detector.go` - Active call detection via audio sessions
- Modified: `main.go` - Uses per-app ducking instead of master volume

---

## [1.3.3] - 2026-02-05 "Speech Overrides Media"

### Changed
- **Restored auto-pause + auto-ducking combination** - The way it was meant to work!
  - Media playing → Auto-pauses transcription (prevents transcribing video audio)
  - You start speaking → Overrides pause, ducks volume to 20%, starts transcribing YOUR speech
  - You stop speaking → Restores volume, resumes pause if media still playing
  - Renamed MediaDetected back to MediaPaused (it's actually pausing now)

### Fixed
- **CRITICAL: Couldn't dictate while media playing** - Fixed stale state variable
  - `currentState` was read at loop start but not updated after state changes
  - When transitioning MediaPaused → Speaking, audio wasn't forwarded to Deepgram
  - Now forwards audio based on Deepgram connection status, not stale state variable

### Technical Details
- Modified: `main.go` - Restored auto-pause behavior with VAD override
  - Media detection auto-pauses but VAD continues monitoring
  - SpeechStarted overrides MediaPaused, ducks volume, starts transcribing
  - SilenceDetected returns to MediaPaused if media still playing
- Fixed: `main.go` - Removed stale currentState check in Speaking handler (line 248-257)
- Modified: `internal/state/manager.go` - Renamed MediaDetected → MediaPaused

---

## [1.3.2] - 2026-02-05 "5ms Media Detection"

### Improved
- **Much faster media detection** - Reduced cache from 25ms to 5ms
  - Checks media status 200 times per second (was 40 times/sec)
  - Near-instant transition when video/audio stops playing
  - Faster return to Active state for quicker dictation readiness

### Technical Details
- Modified: `internal/audio/media_detector.go` - Reduced cacheDuration from 25ms to 5ms

---

## [1.3.1] - 2026-02-05 "Fixed Feedback Loop"

### Fixed
- **CRITICAL: Audio feedback loop** - Fixed ducking triggered by video audio through microphone
  - VAD was detecting video audio through mic as "speech", causing continuous ducking
  - Now only ducks volume when media is NOT playing (prevents feedback loop)
  - When media detected while speaking, immediately restores volume and closes Deepgram connection
  - Volume now properly restores when you stop speaking

### Technical Details
- Modified: `main.go` - Added conditional ducking logic (line 207-209)
  - Only calls `Duck()` when `currentState != state.MediaDetected`
- Modified: `main.go` - Added media detection while speaking handler (lines 191-208)
  - Restores volume immediately when media starts during speech
  - Closes Deepgram connection to prevent transcribing video audio
  - Transitions to MediaDetected state

---

## [1.3.0] - 2026-02-05 "Auto-Ducking"

### Added
- **Auto-ducking Windows volume** - Automatically reduces system volume to 20% when you speak
  - Allows dictation even while videos/music are playing
  - Restores volume when you stop talking
  - Configurable via `duck_volume` in config.toml (default: 0.2 = 20%)
  - Uses Windows IAudioEndpointVolume API for system master volume control

### Changed
- **Removed auto-pause on media** - Now uses auto-ducking instead of pausing
  - Old behavior: paused transcription when media detected
  - New behavior: ducks volume and continues transcribing
  - Simpler and more seamless experience
  - No more interruption when watching videos
- **Renamed MediaPaused → MediaDetected** - Media icon now just visual indicator
  - Amber icon shows when media is playing (visual feedback only)
  - Does not block transcription anymore

### Removed
- `auto_pause_on_media` config option - feature replaced by auto-ducking

### Technical Details
- Implemented: `internal/audio/ducking.go` - Windows IAudioEndpointVolume API integration
  - `Duck()` method reduces volume to configured level (saves original volume)
  - `Restore()` method returns volume to original level
  - Thread-safe with mutex protection
  - Proper COM initialization and resource cleanup
- Modified: `main.go` - Removed auto-pause logic (lines 191-220)
  - Simplified media detection to visual indicator only
  - Audio processing continues even when media is detected
- Modified: `internal/state/manager.go` - Renamed MediaPaused → MediaDetected
  - Updated state constants, String(), and IconColor() methods
- Updated: `version.go` - Bumped to v1.3.0
- Updated: `internal/tray/manager.go` - Updated tooltip and menu version
- Updated: `CHANGELOG.md` - Documented major feature change

---

## [1.2.10] - 2026-02-05 "25ms Media Detection"

### Improved
- **Even faster media resume** - Reduced cache to 25ms (was 100ms)
  - Checks media status up to 40 times per second
  - Near-instant resume when video stops playing

### Technical Details
- Modified: `internal/audio/media_detector.go` - Reduced cache duration from 100ms to 25ms

---

## [1.2.9] - 2026-02-05 "Snappy Media Resume"

### Improved
- **Much faster media resume** - App now resumes within ~100ms after media stops
  - Added 100ms cache to media detection (was checking on every audio chunk)
  - Drastically reduced CPU usage from repeated Windows COM API calls
  - Snappier transition from MediaPaused → Active state

### Technical Details
- Modified: `internal/audio/media_detector.go` - Added caching with 100ms TTL
- New: `checkMediaPlayback()` - Internal function for actual Windows API check
- Media status now cached and only refreshed every 100ms instead of every audio chunk

---

## [1.2.8] - 2026-02-05 "Media Icon + Fix"

### Added
- **New MediaPaused state** - Distinct state for when media/YouTube is playing
- **New media icon** (`mic-media.png`) - Amber/orange icon shows when paused due to media
  - Makes it immediately clear WHY the app is paused
  - Different from grey standby icon (AutoPaused after silence)

### Fixed
- **Media resume issue** - App now reliably comes out of media-paused state
  - Changed condition to check for MediaPaused state directly
  - More reliable than checking AutoPaused + config flag
  - App properly transitions: MediaPaused → Active when video stops

### Technical Details
- Added: `state.MediaPaused` - New app state for media playback detection
- Modified: `internal/state/manager.go` - Added MediaPaused state and icon mapping
- Modified: `internal/tray/icons.go` - Added "media" color case for mic-media.png
- Modified: `main.go` - Use MediaPaused instead of AutoPaused when media detected

---

## [1.2.7] - 2026-02-05 "Fixed Standby Bug"

### Fixed
- **Critical bug: App stuck in standby state** - Fixed issue where app wouldn't come out of AutoPaused state
  - When media playback was detected while Deepgram connection was open, the connection was never closed
  - This left the connection hanging and prevented new speech detection
  - Now properly closes Deepgram connection and restores audio ducking when media is detected
  - App now correctly transitions from AutoPaused → Active → Speaking when you start talking again

### Technical Details
- Modified: `main.go:191-206` - Added Deepgram connection cleanup when media playback detected
- Closes both the Deepgram context and audio channel
- Restores audio ducking to prevent audio conflicts

---

## [1.2.6] - 2026-02-05 "Restart Menu Option"

### Added
- **Restart menu option** in system tray
  - Right-click tray icon and select "🔄 Restart" to restart the application
  - Useful for quickly restarting if something goes wrong
  - Located between Pause/Resume and Exit menu items

### Technical Details
- Modified: `internal/tray/manager.go` - Added restart menu item and handler
- New function: `restartApplication()` - Handles graceful restart with 500ms delay

---

## [1.2.5] - 2026-02-05 "Ctrl+Alt+X Restored"

### Changed
- **Pause hotkey restored to Ctrl+Alt+X** (from Ctrl+Shift+P)
  - Simpler key combination without Shift
  - Works reliably with golang.design/x/hotkey implementation
  - Updated all UI elements and documentation to reflect the change

### Technical Details
- Modified: `main.go` - Changed pause hotkey from Ctrl+Shift+P to Ctrl+Alt+X
- Modified: `internal/tray/manager.go` - Updated tooltip and menu text
- Modified: `version.go` - Updated to v1.2.5

---

## [1.2.1] - 2026-02-05 "Auto-Pause & Hotkeys (Fixed)"

### Fixed
- **Windows Core Audio API implementation** - Fixed compilation errors in media detection
  - Changed from naive browser process detection to proper audio session monitoring
  - Uses IAudioSessionManager2 to enumerate actual audio sessions on speakers
  - Filters out own process ID to avoid false positives
  - Now correctly detects when YouTube, videos, or any media is playing
- Fixed syscall type issues that prevented v1.2.0 from compiling properly

### Technical Details
- Fixed: `internal/audio/media_detector.go` - Proper COM interface calls with correct argument types
- All syscall.Syscall6 calls now use proper uintptr type casting

---

## [1.2.0] - 2026-02-05 "Auto-Pause & Hotkeys"

### Added
- **Auto-pause on media playback** - Automatically pauses dictation when YouTube or other media is playing
  - Prevents transcribing video/audio content
  - Configurable via `auto_pause_on_media` in config.toml
  - Detects Chrome, Edge, Firefox browser processes as media indicators
- **Pause/Resume hotkey** - Press **Ctrl+Alt+Shift+P** to quickly pause/resume dictation
  - No need to click tray icon
  - Works system-wide from any app
- **Multi-hotkey support** - Hotkey manager now supports multiple global hotkeys
  - Ctrl+Alt+Shift+L: Language toggle (English/German)
  - Ctrl+Alt+Shift+P: Pause/Resume toggle

### Changed
- Refactored hotkey manager to support multiple hotkeys simultaneously
- Improved VAD loop to check for media playback before processing audio

### Technical Details
- Added: `internal/audio/media_detector.go` - Windows API media detection
- Modified: `internal/hotkey/hotkey.go` - Multi-hotkey support with sync.Mutex
- Modified: `internal/config/config.go` - Added AutoPauseOnMedia config option
- Modified: `main.go` - Integrated media detection and pause hotkey

---

## [1.1.0] - 2026-02-05 "Dashboard & PNG Icons"

### Added
- **Open Deepgram Dashboard** menu item in system tray
  - Opens https://console.deepgram.com/ for easy access to usage/billing
  - Located between "Open Debug Log" and separator before "Exit"
- **PNG Icon Support** - Icons now load from `icons/` folder at runtime
  - `icons/mic-on.png` - Speaking state (blue/green)
  - `icons/mic-off.png` - Paused state (red)
  - `icons/mic-standby.png` - Standby state (grey)
  - Runtime loading supports multiple paths (IDE and built exe)
- **Version Tracking** - Added `version.go` with version constants
- **This Changelog** - Now tracking all changes between versions

### Changed
- Icon system refactored from programmatic generation to runtime PNG loading
- Icons now use Fluent UI design language
- Simplified icon loading logic with fallback path support

### Technical Details
- Modified: `internal/tray/manager.go` - Added dashboard menu item handler
- Modified: `internal/tray/icons.go` - Switched from ICO generation to PNG loading
- Added: `version.go` - Version tracking
- Added: `CHANGELOG.md` - This file

### Cost Optimization Notes
- Current model: nova-2 ($0.36/hr - 20% cheaper than nova-3)
- Silence threshold: 3000ms (optimized from 5000ms)
- Connection lifecycle properly managed with context cancellation
- Connections open only when speaking, close after silence

---

## [1.0.0] - Previous

### Initial Features
- Real-time speech-to-text using Deepgram API
- System tray icon with state indicators
- Voice Activity Detection (VAD)
- Auto-pause after silence detection
- Audio ducking during dictation
- Keyboard simulation for text insertion
- Language toggle (English/German)
- Auto-start on boot option
- Configuration file management
- Debug logging

### Supported States
- **Active** - Waiting for speech (grey icon)
- **Speaking** - Actively transcribing (blue/green icon)
- **Paused** - User manually paused (red icon)
- **AutoPaused** - Paused after silence (grey icon)
- **MicConflict** - Microphone in use by another app (red icon)
