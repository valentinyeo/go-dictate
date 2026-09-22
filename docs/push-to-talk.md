# Push-to-talk implementation and verification

Push-to-talk is opt-in through General Options or `[audio] push_to_talk = true`. It reserves Ctrl+Alt+Space. `RegisterHotKey` blocks while idle; release checks run every 20 ms only during a hold. The capture device and its context are uninitialized on release. The startup path skips capture when this mode is saved.

The state manager gates microphone access. Automatic VAD, media, watchdog, and default-device transitions cannot reopen it while idle. Ctrl+Alt+X cancels the recording; resume waits for a fresh press. New presses during finalization are ignored. Each deliberate hold bypasses VAD and meeting auto-pause, uses final-only Nova-3 transcription, and retains the selected formatting settings. The microphone opens again for the next hold, which adds device startup latency.

Release stops capture synchronously, drains the remaining audio, and closes the speech client's input channel. The client sends [Deepgram CloseStream](https://developers.deepgram.com/docs/close-stream) and waits for final results. Audio buffered before connection setup survives an early release. Text waits for all shortcut modifiers to be released before delivery. A recording has a five-minute limit; finalization has a 15-second limit, including connection setup. Mode changes and manual pause cancel pending delivery.

## Automated checks

- `go test ./...` passes, including state transitions, key-repeat suppression at the state layer, config persistence and legacy defaults, buffered audio on release, cancellation on pause, and a local WebSocket test for CloseStream/final results. No API key is used by these tests.
- `GO_DICTATE_TEST_MIC=1 go test ./internal/audio -run TestCaptureReleasesDevice` is an opt-in Windows hardware check. It opened and released the default microphone twice, verified that callbacks stopped, and repeated Stop safely. It does not store audio or send it to a speech service.
- The AMD64 release build passes and its PE subsystem is Windows GUI, so opening it does not create a console window.
- The installed Zig/Go toolchain cannot run the race detector: its ThreadSanitizer runtime fails memory allocation at startup. `go vet ./...` also reports unsafe-pointer warnings in existing Windows COM/clipboard code. These checks are not reported as passing.

## Manual checks still needed

1. Quit any older Go Dictate instance, start this build, enable Push to Talk, and confirm Windows no longer shows Go Dictate using the microphone while idle.
2. Hold Ctrl+Alt+Space, pause briefly for microphone startup, speak, and release all shortcut keys. Check that the last words appear in the intended editor. Repeat with a short phrase and in a terminal.
3. Restart with the mode enabled and confirm capture stays off. Check pause/resume, changing microphones while idle, sleep/wake, and disabling the mode to restore continuous listening.
4. Compare battery use over a normal session. No battery-life improvement has been measured yet.
