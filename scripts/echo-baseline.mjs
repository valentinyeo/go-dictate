import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const target = resolve(process.argv[2]);
const baseline = execFileSync('git', ['show', 'v1.8.138:internal/audio/capture.go'], { encoding: 'utf8' });
assert.equal(readFileSync(resolve(target, 'internal/audio/capture.go'), 'utf8'), baseline);
const start = baseline.indexOf('\tonRecvFrames := func(');
const end = baseline.indexOf('\n\tdevice, err :=', start);
assert(start > 0 && end > start);
const callback = baseline.slice(start, end);
const body = callback.slice(callback.indexOf('{') + 1, callback.lastIndexOf('}'));
assert(body.includes('case audioChunks <- samples:'));
assert(!body.includes('echo.filter'));
const fields = '\n\taudioChunks chan<- []int16\n\techo *playbackReference\n';
const method = '\nfunc (c *Capture) receiveFrames(pOutputSample, pInputSamples []byte, framecount uint32) {\n\taudioChunks := c.audioChunks\n' + body + '\n}\n';
writeFileSync(resolve(target, 'internal/audio/capture.go'), baseline.replace('type Capture struct {', 'type Capture struct {' + fields) + method);
for (const file of ['echo.go', 'echo_test.go']) {
  writeFileSync(resolve(target, 'internal/audio', file), readFileSync('internal/audio/' + file));
}
console.log('BASELINE CALLBACK: exact v1.8.138 conversion, raw RMS tracking and channel send; only fields and a callable copy added for hardware-free replay');
