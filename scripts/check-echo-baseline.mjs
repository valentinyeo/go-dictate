import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';

assert.equal(process.env.GITHUB_ACTIONS, 'true', 'Go regression replay is restricted to GitHub Actions');
const result = spawnSync('go', ['test', '-v', './internal/audio', '-run', '^TestCaptureEcho'], { cwd: 'baseline', encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
process.stdout.write(result.stdout || '');
process.stderr.write(result.stderr || '');
assert.ifError(result.error);
assert.equal(result.status, 1, 'baseline must compile and fail the echo regressions');
const output = (result.stdout || '') + (result.stderr || '');
const failed = [...output.matchAll(/^--- FAIL: (\w+)/gm)].map(match => match[1]).sort();
assert.deepEqual(failed, ['TestCaptureEchoColoredRoom', 'TestCaptureEchoPlaybackOnly', 'TestCaptureEchoSimultaneousVoice', 'TestCaptureEchoStreamingTransitions']);
assert(output.includes('--- PASS: TestCaptureEchoVoiceWithoutPlaybackMatch'));
assert(output.includes('playback reached VAD: residual RMS'));
assert(output.includes("mixed output is not the user's voice"));
assert(!output.includes('panic:'));
assert(!output.includes('[build failed]'));
console.log('BASELINE REGRESSION CONTROL VERIFIED: released capture fails all four echo fixtures and preserves unrelated voice');
