# Repository Guidelines

## Project Structure & Module Organization

This is a Windows tray dictation app written in Go. The entry point is `main.go`, with build metadata in `version.go`. Internal packages live under `internal/`: `audio` handles capture, VAD, media detection, and ducking; `deepgram` and `vosk` implement speech-to-text backends; `stt` defines the shared engine interface; `tray` owns the systray UI; `config`, `state`, `hotkey`, `keyboard`, `notify`, and `logger` contain supporting Windows behavior. Assets are in `icons/`, `resources/`, `winres/`, and `downloads/`. Installer and build files are at the repo root, including `installer.nsi`, `build.bat`, `build-arm.bat`, and `build-installer.bat`.

## Build, Test, and Development Commands

- `go mod download`: fetch Go dependencies.
- `go build -ldflags="-s -w -H windowsgui" -trimpath -o go-dictate.exe .`: build the release GUI executable.
- `.\build.bat`: Windows AMD64 release build and versioned copy.
- `.\build-arm.bat`: Windows ARM64 build; requires an ARM64-capable GCC toolchain.
- `go test ./...`: run all Go tests. This repository currently has no `*_test.go` files, but new code should keep this command passing.
- `.\build-installer.bat`: build the NSIS installer after producing the application binary.

CGO must be enabled for normal builds, and MinGW-w64 or an equivalent GCC must be available on `PATH`.

## Coding Style & Naming Conventions

Run `gofmt` on edited Go files before committing. Use standard Go naming: exported identifiers in `PascalCase`, unexported identifiers in `camelCase`, and package names that are short lowercase nouns. Keep Windows-specific APIs isolated in the relevant internal package. When touching COM-based audio code, keep `runtime.LockOSThread()` / `UnlockOSThread()` around COM calls.

## Testing Guidelines

Place tests next to the package under test and name files `*_test.go`. Prefer focused tests for pure logic in packages such as `config`, `state`, `deepgram`, and `audio` helpers. Hardware, tray, hotkey, and Windows integration paths may need manual verification; document that verification in the PR when automated coverage is not practical.

## Commit & Pull Request Guidelines

Git history uses versioned commit subjects such as `v1.8.94: Fix duplicated words after Deepgram interim regression`. For user-facing code changes, increment `version.go`, update `CHANGELOG.md`, and keep tray version text in sync. PRs should include a concise behavior summary, build/test results, linked issues when applicable, and screenshots or logs for tray/UI or audio-state changes.

## Security & Configuration Tips

Do not commit real Deepgram API keys or local `%APPDATA%\dictation\config.toml` contents. Use `config.sample.toml` for safe examples. Preserve the executable name `go-dictate.exe`; shortcuts and user workflows depend on it.


<claude-mem-context>
# Memory Context

# [go-dictate] recent context, 2026-05-12 9:45pm GMT+2

Legend: 🎯session 🔴bugfix 🟣feature 🔄refactor ✅change 🔵discovery ⚖️decision 🚨security_alert 🔐security_note
Format: ID TIME TYPE TITLE
Fetch details: get_observations([IDs]) | Search: mem-search skill

Stats: 50 obs (15,098t read) | 80,243t work | 81% savings

### May 8, 2026
S21 Investigate and fix bad dictation quality (words recognized incorrectly like "generally speaking" → nonsense); add user-friendly options for model and quality/latency tradeoffs (May 8, 11:32 PM)
### May 9, 2026
S22 Understand and review how Smart Format, Punctuation, Keyterms, and Filler Words toggles are implemented in go-dictate's Deepgram integration and tray menu (May 9, 12:19 AM)
S30 Add speech profiles (Terminal/Agent and Email/Writing presets) to simplify dictation configuration and tray menu restructuring (May 9, 12:24 AM)
S34 Evaluating feasibility and approaches for integrating Whisper offline speech recognition as alternative to existing Vosk implementation (May 9, 1:23 AM)
S60 Debug failed dictation attempt by examining application logs to determine what went wrong when speech was not transcribed (May 9, 3:25 PM)
### May 11, 2026
S61 Debug failed dictation attempt and identify root causes and fixes for why Deepgram returned empty transcripts despite VAD detection (May 11, 12:39 PM)
S63 Debug failed dictation attempt and implement fixes for offline mode switching and Vosk preparation coordination (May 11, 12:40 PM)
S65 Debug failed dictation and implement offline provider mode switching with proper state synchronization (May 11, 12:51 PM)
102 12:53p 🔄 Integrate prepareOffline() with state machine for cancellation and queueing support
107 12:55p ✅ Integrate offline preparation state management into speech profile switching
108 12:58p 🔄 Centralize version management by injecting version constants into tray manager
109 " 🔵 Build failure: baseTooltip() method not implemented
110 12:59p 🔵 main.go tray.NewManager call missing required version parameters
111 " 🔴 Implement baseTooltip() method to fix missing method compilation error
112 " 🔵 Compilation error confirms main.go requires version parameter updates
113 1:00p 🔴 Update main.go to pass version parameters to tray.NewManager()
114 1:01p ✅ Cancel pending offline preparation when applying speech profiles
116 1:02p ✅ Update version constants to 1.8.108 with new build date and summary
117 1:03p ✅ Add CHANGELOG entry for version 1.8.108 with offline preparation and version management fixes
118 1:04p ✅ Built and deployed version 1.8.108 with offline preparation state management
S68 Logging improvements for audio/STT diagnostics to enable better future troubleshooting of transcription failures (May 11, 1:04 PM)
135 2:03p 🔵 VAD and STT manager implementation structure identified
136 2:04p 🔵 Existing logging and diagnostics infrastructure examined
138 " 🔵 Deepgram streaming response processing with interim/final transcript handling
141 2:07p 🔵 Deepgram response types expose timing and confidence metadata
142 " 🔵 Vosk engine only emits final results, unlike Deepgram interim support
143 " 🔵 State management via Manager with AppState type
144 2:08p 🔵 STT reset tracking system with categorized reset reasons
145 2:11p 🟣 Utterance-level diagnostics tracking system implemented
146 2:14p ✅ STT reset tracking infrastructure added to main.go
147 " 🔵 Build failure: unused diagnostics import in main.go
148 2:16p ✅ Diagnostics configuration added to Config struct
149 2:17p 🟣 Tray menu UI added for save_failed_audio setting
150 2:21p 🔵 Settings change communication via SettingsChangedCh channel identified
151 2:22p 🟣 Diagnostics integration infrastructure added to VAD/STT manager
152 " 🔵 Build errors: syntax error in config.go and unused helper functions in main.go
153 2:23p 🔴 Fixed fmt.Sprintf format string error in config.go comment
154 2:26p ✅ STT reset tracking integrated into tray settings change and language hotkey
155 2:28p 🟣 Diagnostics integration completed across VAD/STT event flow
156 " 🔵 Build succeeded after diagnostics integration completion
157 2:29p 🔴 Fixed utterance end reason handling for delayed/scheduled ends
158 " 🔵 Code formatting and build verification passed
159 2:30p ✅ Diagnostics integrated into normal-mode silence detection path
160 2:31p 🔵 Integration verification: diagnostics system complete and verified
161 " 🔵 Utterance-level diagnostics implementation summary verified
162 2:32p ✅ Version bumped to 1.8.109 with diagnostics feature annotation
163 2:33p ✅ Config migration logic added for diagnostics.save_failed_audio setting
164 " 🔵 Utterance diagnostics implementation complete and verified
165 " 🔵 Utterance diagnostics implementation complete and release-ready
166 2:34p ✅ Sample config updated with diagnostics section
167 2:35p 🔄 Diagnostics tracker refactored for explicit utterance tracking
168 " 🔵 Diagnostics system complete, refactored, and verified
169 " 🔵 Utterance diagnostics implementation fully complete and verified
170 2:36p ✅ Async STT result handling improved for zero-latency vs normal modes
171 " 🔵 Utterance diagnostics system complete and final build verified
172 " 🔵 Utterance diagnostics implementation delivered and verified complete
173 " ✅ CHANGELOG.md updated with version 1.8.109 diagnostics features
174 2:37p 🔵 Utterance diagnostics implementation complete and production-ready
175 " 🔵 Version 1.8.109 built, deployed, and running successfully
S70 Implement utterance-level diagnostics logging to help troubleshoot missed dictation and transcription failures (May 11, 2:37 PM)
**Investigated**: Examined existing VAD/STT manager architecture, audio capture pipeline, Deepgram and Vosk streaming implementations, logger infrastructure, configuration system, and tray UI framework to identify integration points for diagnostics tracking

**Learned**: Utterance diagnostics requires capturing: (1) VAD/STT context at speech start (provider, model, language, mic, connection state, VAD parameters); (2) audio metrics per utterance (chunk counts, RMS statistics); (3) text timing (when first/last STT results arrive); (4) STT reset correlation (tracking if audio watchdog/connection resets occurred within ±30s of utterance); (5) proper handling of async text delivery in zero-latency mode where new speech can start before previous utterance completes

**Completed**: Complete utterance-level diagnostics system implemented and deployed: (1) internal/diagnostics/utterance.go with Tracker managing Utterance lifecycle; (2) [UTT] logging format capturing provider/model/mic/connection/duration/chunks/RMS/text-timing/reset-context for both utterance start and result events; (3) Configuration with DiagnosticsConfig.SaveFailedAudio (opt-in, defaults to false); (4) Tray UI checkbox under General Options for user control; (5) STT reset tracking integrated throughout application (tray settings, hotkeys, audio watchdog); (6) Full integration into VAD/STT manager event flow (speech start, audio chunks, text events, silence, errors); (7) Optional failed-utterance WAV capture (60s max, saved to %APPDATA%\dictation\failed-utterances); (8) Backward-compatible config migration; (9) Version 1.8.109 "Add dictation diagnostics" built, deployed, and verified running

**Next Steps**: Application running with new diagnostics active. Awaiting real-world testing by user. Next steps dependent on user feedback: if transcription issues occur, user will provide [UTT] log lines and surrounding context for analysis


Access 80k tokens of past work via get_observations([IDs]) or mem-search skill.
</claude-mem-context>