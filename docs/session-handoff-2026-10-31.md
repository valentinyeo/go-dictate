# Session handoff — 2026-10-31

Resume this from a VPS checkout. Read `CLAUDE.md` and `AGENTS.md` first. Then verify `git status` and `version.go` before changing code.

## Objective

Improve go-dictate, check Deepgram for missing features, add Ctrl+Space push-to-talk, strip comments, and stay hyper-aware of Deepgram spend (no always-on billed audio).

Completion for the original ask:

1. Review delivered (improvements ranked).
2. Deepgram gaps listed.
3. Push-to-talk: already on `master` (hold Ctrl+Space → finals → type on release).
4. Comments: already stripped on `master` (`#10`, CI no-comments).
5. Spend controls: confirmed in code (see below). Code changes from this laptop session: none yet.

## Workspace

- Repo: `go-dictate` (Windows tray dictation, Deepgram Nova-3 only).
- Branch at handoff: `master` @ `4918c69` (matches `origin/master`).
- `version.go`: **1.8.136** "Uninterrupted Dictation" (remote also has tags through `v1.8.140`; `version.go` on master is still 1.8.136).
- Untracked local noise only: `go-dictate.exe.prev*` (do not commit).

## Settled facts (do not re-litigate)

### Push-to-talk

Already shipped. Hold Ctrl+Space, stream finals, type joined text after modifiers release.

Key files: `push_to_talk.go`, `docs/push-to-talk.md`, `internal/hotkey/hotkey.go` (`RegisterHold`), `internal/state/manager.go` (`Hold`, `CaptureAllowed`), tray radio in `internal/tray/manager.go`.

Gaps left (polish, not missing plumbing):

- Ctrl+Space is registered even in Always-on (steals the chord; `Hold` no-ops when PTT off).
- Mic cold-start latency on each hold.
- No distinct Finishing icon tint.

### Comments

Already deleted repo-wide. `.github/workflows/no-comments.yml` + `cmd/strip-comments`. Do not re-add narrative comments.

### Deepgram spend controls (confirmed 2026-10-31)

**Audio is what the code treats as billed. Socket open ≠ audio billed.**

| Control | Behavior | Where |
|--------|----------|--------|
| No dial at idle | Socket opens on VAD `SpeechStarted` or PTT hold | `main.go`, `push_to_talk.go` |
| StreamGate | Speech + ~1.5 s quiet tail (~19×80 ms), then hold | `internal/audio/stream_gate.go`; `main.go` constructs with `(1500+80-1)/80` |
| KeepAlive | Text every 3 s after 3 s without audio; repo asserts unbilled | `internal/deepgram/client.go`; tests in `client_test.go` / `lifetime_test.go` |
| Ultra Fast | Keeps socket across silence (default on) | `effectiveZeroLatency` = `ZeroLatency && !BatteryBudgetSaver` |
| Normal / Saver | Close socket on silence | `main.go` `SilenceDetected` branch |
| Mic release | Closes Ultra Fast session immediately | `StreamLifetime.SetMicrophone(false)` |
| Ghost speech | Close after 15 s no text (8 s Saver), 30 s VAD cooldown | `main.go` |
| Empty utterances | 3 empty Ultra Fast silences → reconnect | `main.go` |
| 90 s max_age | Reconnect only when **next** utterance starts if socket older than 90 s | `lifetime.go` `BeginUtterance` (1.8.136 removed 30 s idle close) |
| Saver | No persistence, no keyterms, silence ≤1500 ms, ghost 8 s | tray + `effectiveZeroLatency` / `effectiveKeyterms` |
| PTT | Mic+Deepgram only while held; every chunk including silence; `CloseStream` on release; 5 min cap | `push_to_talk.go` |

**Residual burn risks**

- Default Ultra Fast can leave a socket up all day. Silent gaps should be KeepAlive-only. Connection/KeepAlive pricing was **not** checked against a live Deepgram invoice.
- Ambient speech that transcribes bypasses ghost-speech.
- Echo gate / Ignore System Audio stop once state is `Speaking`.
- Long PTT holds bill continuous audio including silence.
- Live `%APPDATA%\dictation\config.toml` may differ from defaults.

**Strongest idle guarantee for the user:** Push-to-talk, or Ultra Fast off / Battery & Budget Saver on, plus manual pause when idle.

### Deepgram features not wired

Worth considering later (not started):

1. `dictation=true` — spoken punctuation (Email/Writing).
2. `numerals=true` — "nine hundred" → `900` (Terminal/Agent).
3. `utterance_end_ms` — server end-of-utterance with interims.
4. Mid-stream `Configure` — update keyterms without reconnect.
5. `SpeechStarted` events — optional; local VAD already exists.

Already used: Nova-3, punctuate, smart_format, interim+endpointing (Ultra Fast), filler_words, keyterm, KeepAlive, CloseStream, EU host, `tag=go-dictate`.

## Ranked next work (from review)

Ship in this order unless the operator redirects:

1. **Register Ctrl+Space only when PTT is enabled** (unregister on Always-on). Highest daily pain for Always-on users.
2. Cut PTT mic open latency (warm capture or clearer listening UX).
3. Tray: silence threshold + paste-vs-type without editing TOML.
4. Configurable PTT hotkey.
5. Distinct `PushToTalkFinishing` icon/tooltip.
6. Optional Deepgram `dictation` / `numerals` toggles (profile-aware).
7. Docs cleanup (Vosk / nova-2 / 700 ms Ultra Fast silence still mentioned in stale guides).
8. Extract `vadAndSTTManager` from `main.go` (maintainability; larger PR).

Do **not** rebuild PTT from scratch. Do **not** weaken StreamGate / KeepAlive-without-audio / mic-release close without an explicit spend review.

## Constraints for any code change

- Executable name stays `go-dictate.exe`.
- Bump `version.go` + `CHANGELOG.md` per release; commit subject `vX.X.X: …`.
- Build with `build.bat` (Zig CGO, `CGO_LDFLAGS=-Wl,--subsystem,windows`).
- After build: stop `go-dictate`, wait 3 s, start new exe.
- No code comments (CI enforced).
- Prefer smallest diff. Protect billing controls above.

## First actions on VPS resume

1. `git pull` on `master` (include this handoff commit).
2. Confirm tray: mode (Always on vs PTT), Ultra Fast, Battery & Budget Saver.
3. Optional: skim `%APPDATA%\dictation\debug.log` for `[Usage]` and `[STT] Closed persistent connection` lines.
4. Implement item 1 (scope Ctrl+Space to PTT) unless the operator picks something else.
5. Version bump, build, launch, commit, push.

## Paste this into the next session

```
Read docs/session-handoff-2026-10-31.md, CLAUDE.md, and AGENTS.md.
Verify git HEAD and that master is current.
Continue go-dictate from the handoff: start with registering Ctrl+Space only when push-to-talk is enabled, unless I say otherwise.
Stay hyper-aware of Deepgram spend controls (StreamGate, KeepAlive vs audio, mic release, Saver, PTT). Do not weaken them.
/poteto-mode
```

## Session notes

- Laptop session pulled `master` from behind 4 commits through `4918c69`.
- Investigation playbook used for spend confirmation; Opus explainer hit quota; synthesis used explorer findings + direct file reads.
- No feature PR opened from this laptop session.
