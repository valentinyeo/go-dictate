# echo-probe

Standalone Windows recorder; it is not linked into `go-dictate.exe`. Download
`echo-probe.exe` from the `echo-probe` GitHub Actions artifact.

Stop GoDictate and unrelated playback first. Record only consented scripted
speech and licensed/public-domain playback. This tool makes no network calls.

```powershell
.\echo-probe.exe -list
.\echo-probe.exe -mic "KLIM" -seconds 60
.\echo-probe.exe -mic "KLIM Talk" -seconds 30 -out C:\Temp\echo-take-1
```

The microphone substring is case-insensitive and must match exactly one capture
device. Loopback records the default playback endpoint selected at launch; ensure
that the browser uses that endpoint. Devices are pinned for the take, not silently
rerouted. `-list` prints names, IDs and default status without recording.

Outputs default to `%TEMP%\echo-probe\<UTC timestamp>`. Existing output files are
never overwritten. Ctrl+C stops early and saves the take. Both WAVs contain mono
16 kHz signed little-endian PCM16. Audio is buffered in RAM until recording stops
(about 64 KB/second for the pair); keep takes short.

`manifest.json` records endpoint names/IDs, UTC start/end, per-stream start/stop,
frames, callback counts, first-callback UnixNano timestamps, their signed offset,
and delivery gaps greater than 100 ms, including initial/trailing gaps. Empty
streams retain zero first-callback timestamps and a null offset; they cause a
nonzero exit after outputs are saved. Callback input errors also cause failure.

The summary reports elapsed seconds, normalized mic/render RMS, estimated mic
lag, and peak mean-centered normalized envelope correlation. The search uses
10 ms RMS envelopes, lags from 0 through 300 ms, and at least 500 ms of overlap.
`mic_delay_ms=-1` means there was insufficient varying signal for an estimate.
High correlation alone does not establish echo-only speech.

Alignment uses the first callback's wall clock minus that callback's frame
count/sample rate to approximate each WAV's first sample time. It is not hardware
synchronization: malgo exposes neither WASAPI QPC timestamps nor discontinuity
flags here. WAVs contain delivered frames only, without gap padding or clock-drift
correction. Delivery gaps are reported, and the summary warns that their delay
estimate is unreliable. Callback scheduling latency can also bias the estimate.
Do not use these takes as sample-accurate AEC fixtures without further alignment.

The pure Go correlation implementation and synthetic tests are separate from the
Windows-only recorder. GitHub Actions runs the tests, builds this executable,
and uploads it; no local build or test is required by the recorder workflow.
