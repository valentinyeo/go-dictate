# Changelog

All notable changes to the Dictation App will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.8.126] - 2026-09-22

### Changed
- Put Push to talk and Always on directly in the tray's right-click menu, with the current mode checked. Selecting either mode saves the choice and takes effect immediately; selecting the checked mode leaves the recording alone.
- Removed the nested push-to-talk checkbox from General Options.

---

## [1.8.125] - 2026-09-22

### Changed
- Push-to-talk now uses Ctrl+Space for a shortcut that can be held with one hand. Updated tray text, configuration help, and documentation.
- Log successful push-to-talk shortcut registration so startup checks can confirm the shortcut is available.

---

## [1.8.124] - 2026-09-22

### Added
- Push to Talk in General Options. Hold Ctrl+Alt+Space to record and release to finish. Capture is closed while idle, including startup, so Windows can stop microphone processing between recordings.
- Push-to-talk records quiet speech without VAD gating and inserts final text after the shortcut modifiers are released. The mode persists across restarts and profile changes; Ctrl+Alt+X still pauses it.
- Tray status distinguishes ready, recording, and finishing. A five-minute limit releases capture if a key remains held. New recordings wait until the previous transcript finishes.

### Fixed
- Finish the Deepgram stream with CloseStream on release, preserving buffered audio when a short recording ends before the network connection opens.
- Prevent watchdog and default-device checks from reopening an idle push-to-talk microphone. Pausing or changing mode during a recording cancels its text delivery.

---

## [1.8.123] - 2026-07-22

### Added
- **Battery & Budget Saver** in General Options. While enabled, it uses final-only Nova-3 requests, caps trailing silence at 1.5 seconds, abandons no-text requests after 8 seconds, and skips Deepgram keyterm prompting to reduce battery, network use, and transcription charges.

### Changed
- Deepgram transcription is now pinned to **Nova-3**, removing the unused Flux model choice and simplifying the tray and configuration.
- Saver state is included in utterance diagnostics so cost-sensitive behavior is visible in `debug.log`.

### Removed
- Removed the Vosk backend, offline preparation/download code, tray controls, configuration, and bundled integration.

---

## [1.8.122] - 2026-07-22

### Added
- **Train / Noisy Travel mode** in the tray menu. It filters low-frequency carriage rumble before VAD and transcription, requires sustained speech for 320 ms, and raises the adaptive speech-start gate so intermittent train noise is less likely to create empty utterances.
- Train mode is independent of the Terminal / Agent and Email / Writing profiles, preserving the user's formatting and latency preferences. Selecting Normal immediately restores the configured VAD behavior.
- Utterance start diagnostics now record the active noise mode, making noisy-environment failures easier to distinguish in `debug.log`.

---

## [1.8.121] - 2026-06-09

### Fixed
- **A console window opened together with the app** (the "debug window") - v1.8.119/1.8.120 were built with Zig as the CGO linker (MinGW gcc is no longer installed). With CGO, the external linker produces the final exe, and `-H windowsgui` was silently lost - the binaries were linked as console apps (PE subsystem 3). The build now also passes `CGO_LDFLAGS=-Wl,--subsystem,windows`, restoring the GUI subsystem (2). `build.bat` and CLAUDE.md updated so future builds can't regress.

---

## [1.8.120] - 2026-06-09

### Fixed
- **Visible terminal/PowerShell windows popping up** - balloon notifications and the helper spawns (open changelog/log/dashboard/sound settings/config, app restart) launched console processes with only `HideWindow`, which Windows 11 ignores when Windows Terminal is the default console host — so a terminal window appeared on every notification. All child-process spawns now also set `CREATE_NO_WINDOW`, which never allocates a console at all. Notifications and helpers still work; nothing is visible.

---

## [1.8.119] - 2026-06-09

### Added
- **Dictation profiles** in the tray menu: Terminal / Agent (fast append-only dictation), Email / Writing (slower, cleaner final transcripts), or Custom. Selecting a profile applies a settings bundle; manual tweaks flip the profile to Custom.
- **Deepgram model picker**: Nova-3 (stable) vs Flux (experimental, lower latency). Audio chunks reduced from 100ms to 80ms per Deepgram's Flux latency recommendation (applies to all providers).
- **Adaptive VAD noise gate**: learns the ambient noise floor during silence (slow upward, fast recovery), debounces speech onset (`speech_start_chunks`, default 2) so single loud chunks (key press, chair bump) no longer open an STT connection — the pre-roll buffer preserves the debounced chunks so no audio is lost — and uses a lower release threshold while speaking so quiet syllables don't chop the utterance. Config: `[vad] adaptive_noise`, `noise_multiplier`, `min_start_threshold`, `speech_start_chunks`.
- **Utterance diagnostics** (`internal/diagnostics`): every utterance logs compact `[UTT n]` start/result lines (provider, connection reuse, RMS/threshold, text latency, char counts, drop counts) to debug.log. Optional "Save Failed Utterance WAVs" tray toggle stores audio of empty-result utterances under `%APPDATA%\dictation\failed-utterances` (privacy-sensitive, off by default).
- Vosk now flushes the final transcript when the audio stream closes (was silently lost before), and logs when a transcript is dropped because the text channel is full.

### Changed
- Balloon notifications are generated via a hidden PowerShell NotifyIcon process.
- Version/tooltip strings now flow from `version.go` through `tray.NewManager` — no more hardcoded version strings scattered through manager.go.

### Fixed (code review of this release)
- Notification launch failures are now logged instead of silently discarded, and line breaks in notification text can no longer break the generated PowerShell script.
- Diagnostics transcript preview no longer splits multi-byte UTF-8 characters (umlauts, emoji) at the 500-byte cap, which produced invalid UTF-8 in debug.log and a missing truncation ellipsis.
- Failed-utterance WAV writer batches all samples into one write instead of ~1M per-sample writes (seconds → milliseconds for a 60s clip).
- VAD atomic float64 diagnostics fields now go through shared load/store helpers instead of 7 copies of the bit-conversion boilerplate.

---

## [1.8.118] - 2026-06-09

### Fixed
- **Ultra Fast word doubling ("dictating dictating", "not happening not happening")** - Deepgram sometimes closes a final EARLIER than the interims it already streamed (interim: `...to see if it's dictating a`, final: `...to see if it's`), then re-delivers the overhang at the start of the next segment (`dictating a lot faster.`). The diff had already typed the overhang from the interim, so the next segment typed it again. The client now detects a final that ends before the typed offset, carries the typed-but-unfinalized overhang, and skips it when the next segment's first interim starts with it (word-boundary checked, case-insensitive, 3s expiry so it can't misfire on a later coincidental match).

### Known limitation
- A doubled word that Deepgram itself transcribes in an interim (`Testing Ultra Ultra fast`) and later corrects in a rewrite cannot be retracted — append-only typing never backspaces. This is inherent to live interim mode.

---

## [1.8.117] - 2026-06-09

### Fixed
- **Nothing appeared when pasting into Claude Code in classic cmd.exe (conhost)** - v1.8.116's paste worked in Notepad and tmux but not in a local Claude Code session hosted by classic conhost: conhost passes Ctrl+V THROUGH to raw-mode apps instead of pasting, and Claude Code treats a bare Ctrl+V as image-paste (no image → no-op). When the focused window is a classic console (`ConsoleWindowClass`), the app now sends conhost's system-menu "Edit > Paste" command (`WM_SYSCOMMAND` / `SC_PASTE` `0xFFF1`) so conhost itself injects the clipboard into the console input — works regardless of the app's input mode. Windows Terminal and GUI apps keep the regular Ctrl+V keystroke.

---

## [1.8.116] - 2026-06-09

### Changed
- **Text is now delivered by clipboard paste (Ctrl+V) instead of keystroke injection** - v1.8.115's VK_SPACE encoding did NOT fix the dropped spaces: reconstruction showed the terminal dropped spaces even from within a single atomic SendInput batch (`' Especially if the outcome is a lot better...'` was sent as one batch; `isalotbetter` appeared on screen). ConPTY drops space key-events from injected batches regardless of encoding, so no keystroke-based method can be reliable. Paste is immune: the terminal emulator itself reads the clipboard and forwards literal bytes to whatever runs inside — cmd.exe, Windows Terminal, and tmux/SSH all receive the exact text without tmux needing any clipboard support of its own. Works for both fast and slow modes; no toggle needed.
- The user's clipboard text is captured before a paste burst and restored ~1.5s after it ends — only if the clipboard still holds our pasted text, so anything you copy mid-dictation is never clobbered. Limitation: non-text clipboard content (images, files) can't be saved/restored across a paste.
- New config (no tray toggle): `[keyboard] injection = "paste"` (default) or `"type"` (legacy per-character key events). Paste automatically falls back to typing if the clipboard is unavailable.

---

## [1.8.115] - 2026-06-09

### Fixed
- **Dropped spaces between words ("cloud code" → "cloudcode")** - Affected BOTH fast and slow modes, so it was never the interim diffing. Reconstructing the actual injected text proved we always sent the space correctly (`and duplicating`, `accuracy of whatever`); the terminal/app was dropping it. Root cause: spaces were injected as `KEYEVENTF_UNICODE` events, which terminals (ConPTY, e.g. the Claude Code prompt) drop under load — while letters come through fine. Spaces are now injected as a real `VK_SPACE` keypress (exactly how Backspace was already handled), which is delivered reliably. Mode-independent fix.

---

## [1.8.114] - 2026-06-09

### Fixed
- **Phrases typed twice ("I still can. I still can dictate")** - With `endpointing=200`, Deepgram emits `is_final:true` responses with empty text at brief endpoints — even in the middle of an utterance. The Ultra Fast handler treated every final the same and reset `dgText`/`typedBy` to zero, so the next interim re-typed the entire phrase from scratch, duplicating what was already on screen. Empty finals are now ignored (no state reset); only a non-empty final or VAD silence ends an utterance.

---

## [1.8.113] - 2026-06-09

### Fixed
- **Words glued together after a thinking pause** - In Ultra Fast mode, pausing >3s triggered `ResetContext()`, which zeroed the keyboard's `lastChar`. The next word was then typed with no leading space and jammed onto the previous text (`opened` + `Let's` → `openedLet's`). A pause is not a new text field, so `ResetContext` now keeps `lastChar` (the next word stays space-separated) and only suppresses the auto-period once — so resuming in a genuinely fresh field still can't inject a stray leading `". "`.

---

## [1.8.112] - 2026-06-09

### Fixed
- **Dropped/garbled keystrokes in terminals (Claude Code prompt)** - Ultra Fast mode emits many tiny text fragments (interim diffs + finals) in rapid bursts. Each was injected as its own `SendInput` call, saturating the input queue of busy windows like the Claude Code terminal, which then dropped spaces (`using it` → `usingit`) or repeated characters (`see` → `ssssee`). The keyboard simulator now coalesces all already-queued fragments into a single `SendInput` batch via a non-blocking drain — far fewer, less-crowded injections, with no added latency.
- **Silently lost keystrokes on blocked injection** - `SendInput` returns the count of events it actually inserted and stops at the first event blocked by another thread; the remainder were silently dropped (only `ret == 0` was checked). Injection now resumes from the returned offset and retries from there (bounded, with a brief back-off only when fully blocked) until every event lands.

---

## [1.8.111] - 2026-05-14

### Fixed
- **Skipped held-back words in Ultra Fast mode** - Fixed the interim holdback diff logic so words like `I`, `way`, `logs`, and `VWO` are emitted when they become stable instead of being skipped.
- **Spacing for held-back words** - Held words that become the first emitted text in a segment now use normal spacing instead of being glued to the previous word.

---

## [1.8.110] - 2026-05-14

### Fixed
- **Reliability-first VAD defaults** - Replaced the overly sensitive fixed VAD behavior with adaptive noise-floor tracking, a safer speech-start threshold, and consecutive speech-chunk debouncing to reduce fan/keyboard false starts.
- **Ultra Fast no longer chops sentences at 700ms** - Ultra Fast mode now keeps the configured silence threshold with a 2500ms minimum, so short pauses are less likely to split or drop sentence endings.
- **Stale Deepgram sessions recover automatically** - Persistent Deepgram connections are recycled after 90 seconds and after repeated empty utterances, preventing the app from staying connected while returning no text.
- **Real-time audio reliability mode** - Disabled Windows EcoQoS power throttling for the app and restored normal process priority to reduce wireless mic callback dropouts.

### Changed
- **Terminal / Agent default is safer** - Hold Back Last Word is now enabled for the Terminal / Agent profile to avoid typing unstable Deepgram interim words.

---

## [1.8.109] - 2026-05-11

### Added
- **Utterance diagnostics for missed dictation** - Each VAD-triggered utterance now logs `[UTT ...]` start/result lines with provider, model, mic, connection reuse, duration, chunks, dropped chunks, RMS levels, text timing, and recent STT reset context.
- **Optional failed-utterance WAV capture** - Added `diagnostics.save_failed_audio` and a tray toggle under `General Options` to save WAV files only when VAD detects speech but no transcript is produced. This is disabled by default for privacy.

---

## [1.8.108] - 2026-05-11

### Fixed
- **Tray provider state stays in sync with actual mode** - Pending Vosk downloads/preparation are now intent-aware. If you switch back to Deepgram while Vosk is still preparing, completion no longer flips the tray/config back to `Offline (Vosk)`.
- **Centralized tray version display** - Tray menu labels and provider tooltips now use the app version constants passed from `main`, preventing hardcoded version drift.

---

## [1.8.107] - 2026-05-09

### Fixed
- **Vosk finalization now drains buffered audio before ending** - On silence, offline mode now closes the Vosk audio channel and lets the recognizer consume queued chunks before calling `FinalResult`. Previously the context was cancelled first, which could discard the utterance and produce no transcript.
- **Vosk empty finals are logged** - Empty Vosk final results now emit a debug log line to make future offline troubleshooting clearer.

---

## [1.8.106] - 2026-05-09

### Fixed
- **Vosk no longer gets stuck in speaking/green while loading** - Vosk models are now preloaded while idle/startup/settings change instead of first loading after speech starts. Large models can take many seconds to map into memory, and loading them during speech made the tray appear stuck.
- **Ghost speech detection disabled for Vosk** - The ghost-speech timeout expects interim text while speaking, which Deepgram provides but Vosk does not. It was killing valid Vosk sessions before final transcripts could be emitted.
- **Vosk model is kept loaded across audio restarts** - Audio-capture reset events no longer unload and reload the same selected Vosk model unnecessarily.

---

## [1.8.105] - 2026-05-09

### Changed
- **Active STT provider visible at the first menu level** - The tray now shows titles like `✓ Deepgram: Nova-3` or `✓ Vosk: Large`, so you do not need to open a submenu to see whether online or offline dictation is active.
- **Deepgram-only options moved under Deepgram** - Punctuation, Smart Format, filler words, holdback, and keyterms now live in the `Deepgram` submenu because Vosk does not use those settings.
- **General Options submenu** - Ultra Fast/Fast Finalize, Audio Ducking, and Auto-pause now live under `General Options` because they are app-level behavior rather than Deepgram/Vosk model settings.

---

## [1.8.104] - 2026-05-09

### Changed
- **Grouped STT provider tray menus** - Deepgram mode/model options now live under `Deepgram`, and Vosk offline/model options now live under `Vosk`.
- **Vosk model naming** - The old `Offline Model` wording is now explicit `Vosk Model: Small` / `Vosk Model: Large`.

### Fixed
- **Vosk Large selection downloads instead of silently falling back** - When Vosk is enabled and the selected model is missing, the tray now prepares/downloads that Vosk model. Offline mode no longer silently falls back to Deepgram when the selected Vosk model is unavailable.

---

## [1.8.103] - 2026-05-09

### Fixed
- **Offline Vosk transcripts now type into the active window** - Vosk produces its final transcript while the STT session is shutting down; the text bridge now stays alive long enough to forward that final result, and the Vosk engine flushes final text whether VAD cancels the context or closes the audio channel.

---

## [1.8.102] - 2026-05-09

### Added
- **Profile submenu** - Right-click tray menu now has `Profile` with `Terminal / Agent`, `Email / Writing`, and read-only `Custom`.
- **Terminal / Agent preset** - Uses Nova-3, Ultra Fast mode, punctuation, no smart format, no filler words, and no holdback for fast append-only dictation.
- **Email / Writing preset** - Uses Nova-3, final-result mode, punctuation, smart format, no filler words, and no holdback for cleaner prose.

### Changed
- **Speech Options submenu** - Detailed toggles now live under `Speech Options` so profiles are the first-level control while advanced settings remain accessible.

---

## [1.8.101] - 2026-05-09

### Added
- **Separate Smart Format tray toggle** - Punctuation now sends Deepgram's `punctuate=true`, while Smart Format separately sends `smart_format=true` for numbers, dates, currency, and related formatting. Smart Format still inherently enables punctuation on Deepgram's side.
- **Edit Keyterms tray shortcut** - Right-click tray menu now has `Edit Keyterms`, which opens the actual `config.toml` so Nova-3 keyterms can be edited quickly.

### Changed
- **Clearer filler-words toggle** - The tray menu now says `Include Filler Words`, matching Deepgram's `filler_words=true` behavior. Leaving it off lets Deepgram strip common fillers such as "uh" and "um".

---

## [1.8.100] - 2026-05-09

### Changed
- **Hold Back Last Word is opt-in** - The holdback feature remains available in the tray menu, but is now marked experimental and defaults off because delayed text is annoying when manually editing or deleting while dictation is still producing output.

---

## [1.8.99] - 2026-05-08

### Added
- **Hold Back Last Word tray toggle** — Right-click tray menu now lets you turn the one-word interim holdback on/off without editing config. This only affects Ultra Fast interim typing; normal mode still waits for final transcripts.

---

## [1.8.98] - 2026-05-08

### Changed
- **Hold back one unstable interim word in Ultra Fast Nova mode** — The app now types all but the newest interim word, then flushes the held word when Nova confirms it in a later interim or final result. This improves live-edge dictation quality without using risky terminal backspace corrections.

---

## [1.8.97] - 2026-05-08

### Added
- **Deepgram model selector in tray menu** — Right-click tray menu now includes `Deepgram Model` with `Nova-3` and `Flux (Experimental)`, so Flux can be tested without editing `config.toml`.

### Changed
- **Nova-3 restored as the practical default** — Flux remains available for low-latency testing, but early testing showed substantially worse dictation accuracy for general speech.

---

## [1.8.96] - 2026-05-08

### Added
- **Experimental Deepgram Flux adapter** — Setting `model = "flux-general-en"` uses Deepgram's `/v2/listen` Flux endpoint and parses `TurnInfo` events (`StartOfTurn`, `Update`, `EndOfTurn`) for Ultra Fast append-only dictation.

### Changed
- **80ms audio chunks** — Capture now uses 1280-frame chunks at 16kHz (~80ms) instead of 1600-frame chunks (~100ms), matching Deepgram's Flux latency recommendation while keeping the same linear16 mono audio format.

---

## [1.8.95] - 2026-05-08

### Fixed
- **Ultra Fast sometimes ignored speech after waking** — Deepgram can send a final transcript without an interim first. The stale-final guard treated those as old results and discarded them, so valid phrases like "Are you back?" never typed. Now final-only results are accepted when fresh audio was just sent, while duplicate stale finals are still suppressed.
- **Lark M2 capture recovery is faster** — When the audio callback goes silent, the watchdog now performs a full capture rebuild immediately instead of spending two failed attempts on lightweight Stop/Start restarts. Any capture restart also resets the persistent STT session so Deepgram is not reused after a mic wake/reconnect.
- **Pre-roll now matches the intended 500ms** — Capture chunks are 100ms, not 20ms, so the previous 25-chunk pre-roll sent up to 2.5s of old silence/audio before each utterance. This is now 5 chunks.

---

## [1.8.94] - 2026-04-28

### Fixed
- **Word duplication in Ultra Fast mode** ("simply simply", "you you" mid-sentence) — When Deepgram regressed an interim (sent a shorter version of already-typed text, e.g. during long sentences with brief pauses where the audio buffer is re-processed), `dgText` shrunk but `typedBy` stayed at the higher value. The next extending interim then computed the suffix from `len(dgText)` and re-emitted bytes between `len(dgText)` and `typedBy` — bytes already on screen. Fix: in the HasPrefix branch, skip past `max(len(dgText), typedBy)` so already-typed text never gets re-emitted.

---

## [1.8.91] - 2026-03-27

### Fixed
- **VAD hysteresis fixes YouTube bleed AND mid-sentence cutoffs** — The real root cause: v1.8.81 lowered the VAD threshold from 0.02 to 0.005, which fixed mid-sentence cutoffs but let YouTube bleed trigger SpeechStarted. The proper fix is hysteresis — two thresholds: a **high start threshold (0.02)** to begin speech (YouTube bleed at 0.005-0.02 can't trigger it), and a **low continue threshold (0.005)** to sustain speech (inter-syllable dips at 0.006-0.016 don't cause premature silence). Removed all previous media detection hacks, duck cooldowns, and RMS filters — the VAD itself now handles everything cleanly.

---

## [1.8.90] - 2026-03-27

### Fixed
- **Duck cooldown breaks feedback loop** — All previous attempts relied on `IsMediaPlaying()` COM detection, which turned out to be unreliable (often returns false even when YouTube is playing). New approach doesn't need media detection at all: after every unduck (Restore), a 2-second cooldown suppresses audio chunks with RMS < 0.025. This breaks the loop at the source (unduck → YouTube bleed → duck → repeat) because the bleed that follows an unduck is always low-RMS and gets filtered during the cooldown window. Real speech (0.03+) still passes through immediately.

---

## [1.8.89] - 2026-03-27

### Fixed
- **Pre-VAD media filter** — Previous approach filtered at SpeechStarted, but that fired 10x/second (each doing expensive COM calls) and YouTube peaks still leaked through. New approach: filter BEFORE the VAD sees the audio. When the ticker detects media playing (cache always fresh now), chunks with RMS < 0.025 are skipped entirely — no SpeechStarted, no ducking, no flicker. Real speech (0.03+ RMS) passes through normally. Zero CPU cost (cached bool + simple RMS math).

---

## [1.8.88] - 2026-03-26

### Fixed
- **Media RMS filter actually working now** — Two bugs in v1.8.87: (1) used `IsMediaPlayingCached()` but the ticker never updates the cache when `duck_enabled=true` — switched to synchronous `IsMediaPlaying()` at SpeechStarted. (2) 3x threshold (0.015) was too low — YouTube bleed peaks at 0.02 RMS. Raised to 5x (0.025) which sits above YouTube bleed (~0.02) and below real speech (~0.03+).

---

## [1.8.87] - 2026-03-26

### Fixed
- **YouTube bleed-through from v1.8.81 threshold change** — v1.8.81 lowered the VAD energy threshold from 0.02 to 0.005 for the Lark M2, which inadvertently let YouTube audio bleed (RMS 0.006-0.015) trigger the VAD. This caused rapid green/white flicker and stuck ducked volumes. Fix: when media is playing and ducking is enabled, apply a 3x energy threshold filter at SpeechStarted — YouTube bleed (low RMS) is silently suppressed, real speech (high RMS 0.02+) ducks and transcribes normally. Restored the original ducking behavior: app stays white, user can dictate over videos.

---

## [1.8.86] - 2026-03-26

### Fixed
- **YouTube media suppression completely reworked** — Previous cache-based approach was fundamentally broken: 3s stale cache meant YouTube audio always triggered VAD before detection caught up, causing green flash + volume ducked to 5%. New approach: `SpeechStarted` now does a **synchronous** `IsMediaPlaying()` check (not cached) — if media is playing, skips straight to `MediaPaused` without ever ducking volume or opening STT. Ducking is completely removed from MediaPaused enter/leave — no reason to lower volume when dictation is paused.

---

## [1.8.85] - 2026-03-26

### Fixed
- **YouTube green flash + stuck 5% volume** — The 1-second ticker was too slow: YouTube audio triggered VAD → SpeechStarted → ducked Edge to 5% → green icon, THEN the ticker caught up and set MediaPaused → amber. But the duck was already done and volume stuck. Fixed by checking `IsMediaPlayingCached()` on the audio chunk path (every 20ms) BEFORE VAD processing, so media is caught instantly. Also blocks ducking in SpeechStarted when media is cached as playing.

---

## [1.8.84] - 2026-03-26

### Fixed
- **YouTube rapid green/white flicker** — Three bugs combined to break media suppression when ducking was enabled: (1) `IsMediaPlaying()` used peak meter which read near-zero after ducking reduced volume to 5%, so media was never detected — added `IsAudioSessionActive()` that checks session state only. (2) VAD still processed audio chunks during `MediaPaused`, so YouTube audio triggered `SpeechStarted` overriding the pause — now skips VAD in `MediaPaused`. (3) `Duck()` saved 5% as "original" when Edge was already ducked from rapid cycles — now detects already-ducked volume and stores 100% instead.

---

## [1.8.83] - 2026-03-26

### Fixed
- **Media auto-pause broken when ducking enabled** — When `duck_enabled=true`, the `!cfg.Audio.DuckEnabled` guard completely disabled media auto-pause, so YouTube videos were never suppressed. Instead, a feedback loop occurred: YouTube audio triggers VAD → duck volume → silence detected → unduck → YouTube loud again → repeat. Fixed by allowing auto-pause to work alongside ducking, and keeping volumes ducked during silence when media is still playing.

---

## [1.8.82] - 2026-03-26

### Fixed
- **Stuck in meeting mode (false positive)** — The Windows Settings app (`windows.immersivecontrolpanel`) registers mic access when you open Sound settings and never clears it (LastUsedTimeStop stays 0). The meeting detector saw this as an active call, permanently pausing dictation. Added it to the excluded apps list along with Windows Sound Recorder.

---

## [1.8.81] - 2026-03-24

### Fixed
- **VAD cutting off mid-sentence on low-gain mics** — The Lark M2 wireless mic's speech RMS (0.02-0.03) was barely above the energy threshold (0.02). Natural inter-syllable dips (0.006-0.016) fell below the threshold, causing the VAD to detect "silence" every 1-2 seconds mid-sentence. Deepgram would finalize prematurely, fragmenting words. Lowered default energy threshold from 0.02 to 0.005 — comfortably above actual silence (0.002) while keeping all mid-word dips as "speech." Added RMS diagnostic logging to SpeechStarted/SilenceDetected events.

---

## [1.8.80] - 2026-03-23

### Fixed
- **Watchdog switching to wrong mic** — When Intel SST mic's hardware noise cancellation produced very low inter-speech RMS (~0.001), the watchdog falsely detected a "dead mic" and fell back to the system default device, which was a phantom device producing zero audio. The app would stop hearing speech until the 1-minute periodic retry switched back. Fix: never fall back to system default when user explicitly selected a device, and lowered the flat-audio RMS threshold from 0.005 to 0.0005 (with 30s tolerance instead of 20s).

---

## [1.8.79] - 2026-03-22

### Fixed
- **Merged-text re-emission during long utterances** — The 2s stale interim reset was too aggressive. During normal speech with brief pauses, Deepgram re-processes the audio buffer and sends back a merged version (no spaces) of already-typed text. The reset cleared tracking, causing the entire merged string to be re-typed. Increased to 10s — Deepgram's `endpointing=200` sends finals within ~200ms, so the timer is just a safety net.

---

## [1.8.78] - 2026-03-21

### Fixed
- **Stale text replay across windows** — In Ultra Fast mode, stale Deepgram finals from a previous utterance were being typed into the new active window. Now discards finals that arrive after state reset until a fresh interim confirms new speech.
- **Leading dot on window switch** — Keyboard context (`lastChar`) was never reset in Ultra Fast mode, causing `. ` to be prepended when dictating in a new context. Now resets after 3+ seconds of silence.

## [1.8.77] - 2026-03-21

### Fixed
- **Dropped spaces in Ultra Fast mode** — Switched keyboard simulator from per-character SendInput calls (with 2ms sleeps) to a single atomic batch SendInput call per text chunk. Windows guarantees no input can interleave within a batch, eliminating dropped keystrokes.

---

## [1.8.68] - 2026-03-20 "Ultra Fast"

### Added
- **Ultra Fast Mode** - New tray menu toggle (`Ultra Fast Mode` checkbox) for zero-latency dictation. When enabled:
  - **Interim results** - Text appears word-by-word as Deepgram hears it, instead of waiting for full sentence processing. Progressive typing with intelligent backspace corrections when Deepgram revises earlier words.
  - **No smart_format** - Skips punctuation/capitalization processing for faster raw output.
  - **Fast endpointing (200ms)** - Deepgram finalizes utterances after just 200ms of silence.
  - **Persistent Deepgram connection** - WebSocket stays open between utterances via KeepAlive, eliminating ~200-400ms connection handshake.
  - **700ms silence threshold** - Reduced from 3000ms default for snappy single-word commands.
  - No additional Deepgram cost — KeepAlive messages carry no audio and aren't billed.
- Ideal for command-line use where short, fast dictation commands are needed.

---

## [1.8.66] - 2026-03-06 "Mic Hotkey"

### Added
- **Ctrl+Alt+M cycles through microphones** - Press the hotkey to switch to the next microphone in the list (wraps around to System Default). Shows a Windows balloon notification with the new mic name for visual confirmation.

---

## [1.8.65] - 2026-03-05 "ARM Ready"

### Added
- **Native ARM64 build** - App now builds natively for Windows ARM64, eliminating x64 emulation overhead.
- **Architecture-aware Vosk DLL download** - Uses `runtime.GOARCH` to select the correct Vosk library. Since Vosk has no ARM64 Windows DLL, offline mode gracefully reports unavailability on ARM64 instead of downloading an incompatible x64 binary.

---

## [1.8.64] - 2026-03-03 "Self Aware"

### Fixed
- **Meeting detector no longer detects itself** - The registry-based mic usage check hardcoded `go-dictate.exe` as the self-exclusion name. Any variant (e.g. debug builds) or stale registry entries from force-killed instances would trigger false "meeting detected" auto-pause. Now uses a prefix-based exclusion.
- **Exclude Beeper from meeting detection** - Beeper (messaging app) holds mic access open at all times, causing permanent false "meeting detected" auto-pause. Added an exclusion list for apps known to keep mic registered without active calls.

---

## [1.8.62] - 2026-03-02 "No More Nukes"

### Removed
- **Removed Windows Audio service restart ("nuclear option")** - The audio watchdog would restart the entire Windows Audio service (`Audiosrv`) after 5 failed mic recovery attempts. This spawned elevated PowerShell processes that flashed shell windows on screen and disrupted all system audio. The watchdog now simply enters monitor mode and retries periodically — the mic usually recovers on its own.
- **Tray "Reset Audio" no longer touches Windows Audio service** - Now just restarts the app to get a fresh audio capture, without system-wide audio disruption.

---

## [1.8.61] - 2026-03-02 "Wake Resilient"

### Fixed
- **Watchdog recovers mic after sleep/wake while paused** - When a wake event occurred while the app was paused, the audio device would be dead on unpause. The watchdog burned through all 5 restart attempts on stale hardware, then gave up. Now defers the wake restart to unpause time (with 5s settle delay) so the mic re-initializes properly.
- **Faster monitor mode retries** - Reduced watchdog monitor-mode retry interval from 5 minutes to 1 minute, so the mic recovers faster if the initial restart attempts fail.

---

## [1.8.60] - 2026-02-26 "Comma Aware"

### Fixed
- **Don't insert period after commas** - When Deepgram splits an utterance at a comma (e.g., `"Valentin Yeo,"` then `"I work for..."`), the auto-period feature would produce `"Yeo,. I work"`. Now treats commas as continuation punctuation.

---

## [1.8.59] - 2026-02-26 "Clean Starts"

### Fixed
- **Fix stray period at start of new speech sessions** - The auto-period insertion (v1.8.55) would prepend `. ` to the first utterance of a new speech session if the previous session's last utterance lacked trailing punctuation. Now resets context on silence detection so each speech session starts clean.

---

## [1.8.58] - 2026-02-26 "Keyterm Boost"

### Added
- **Personal keyterms** - Added "Valentin Yeo", "Hypertask", and "Inne" to default keyterms for better recognition of names and business terms.

---

## [1.8.57] - 2026-02-26 "Keyterm Boost"

### Added
- **Deepgram keyterm prompting for Nova-3** - Custom vocabulary terms are now sent as `keyterm` parameters to boost recognition accuracy up to 90%. Default terms include "Claude Code", "exe", "GitHub", "Deepgram", and "Vosk". Add your own in config.toml under `[deepgram] keyterms`.

---

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
