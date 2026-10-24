import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

const git = (...args) => execFileSync('git', args, { encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'] });
const protectedFiles = ['main.go', ...git('ls-tree', '-r', '--name-only', 'v1.8.138', 'internal/deepgram', 'internal/config', 'internal/tray').trim().split('\n'), ...git('ls-tree', '-r', '--name-only', 'v1.8.138', 'internal/audio').trim().split('\n').filter(file => file !== 'internal/audio/capture.go')];
function unchanged(actual, expected) {
  assert.equal(actual.replaceAll("\r\n", "\n"), expected.replaceAll("\r\n", "\n"), 'protected lifecycle, VAD, media filter, configuration or ducking changed');
}
for (const file of protectedFiles) {
  const expected = git('show', 'v1.8.138:' + file);
  unchanged(readFileSync(file, 'utf8'), expected);
}
const main = readFileSync('main.go', 'utf8');
assert.throws(() => unchanged(main.replace('close(sttAudioTx)', 'sttAudioTx = nil'), main));
assert.throws(() => unchanged(main.replace('silenceMs = 1500', 'silenceMs = 3000'), main));
const comments = source => {
  source = source.replaceAll('\r\n', '\n');
  const found = new Map();
  const tokens = /"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`[^`]*`|(\/\/[^\n]*|\/\*[\s\S]*?\*\/)/g;
  for (const match of source.matchAll(tokens)) {
    if (match[1]) found.set(match[1], (found.get(match[1]) || 0) + 1);
  }
  return found;
};
function noAddedComments(source, baseline) {
  const old = comments(baseline);
  for (const [text, count] of comments(source)) assert(count <= (old.get(text) || 0), 'added code comment: ' + text);
}
assert.throws(() => noAddedComments('const x = 1; // forbidden', 'const x = 1;'));
assert.throws(() => noAddedComments('/* forbidden */', ''));
noAddedComments('const url = "https://example.invalid";', '');
const files = git('diff', '--name-only', 'v1.8.138', '--', '*.go').trim().split('\n').filter(Boolean);
for (const file of files) {
  let baseline = '';
  try { baseline = git('show', 'v1.8.138:' + file); } catch {}
  noAddedComments(readFileSync(file, 'utf8'), baseline);
}
assert.match(readFileSync('version.go', 'utf8'), /Version\s*= "1\.8\.139"/);
assert.match(readFileSync('CHANGELOG.md', 'utf8'), /## \[1\.8\.139\]/);
console.log('ECHO SCOPE VERIFIED: lifecycle and ducking byte-identical; close and 1.5s mutation controls rejected; no added code comments; release metadata present');
