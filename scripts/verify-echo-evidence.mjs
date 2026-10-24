import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

const repo = 'valentinyeo/go-dictate';
const branch = 'ceo/IGSH-710-echo';
const title = 'IGSH-710: echo gate keeps laptop audio out of the transcript';
const exec = (command, args) => execFileSync(command, args, { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024, stdio: ['pipe', 'pipe', 'pipe'] });
const git = (...args) => exec('git', args).trim();
const gh = (...args) => exec('gh', args);
const [mode, id] = process.argv.slice(2);
const regressions = ['TestCaptureEchoPlaybackOnly', 'TestCaptureEchoSimultaneousVoice', 'TestCaptureEchoColoredRoom', 'TestCaptureEchoStreamingTransitions'];
if (mode === 'red' || mode === 'green') {
  assert.match(id, /^\d+$/);
  const run = JSON.parse(gh('run', 'view', id, '--repo', repo, '--json', 'status,conclusion,headSha,jobs,url'));
  assert.equal(run.status, 'completed');
  const log = gh('run', 'view', id, '--repo', repo, '--log');
  if (mode === 'red') {
    assert.equal(run.conclusion, 'failure');
    const job = run.jobs.find(job => job.name === 'baseline-red');
    assert.equal(job?.conclusion, 'failure');
    assert.equal(job.steps.find(step => step.conclusion === 'failure')?.name, 'Replay fixtures through released capture callback');
    assert(log.includes('BASELINE CALLBACK: exact v1.8.138'));
    for (const test of regressions) assert(log.includes('--- FAIL: ' + test));
    assert(log.includes('--- PASS: TestCaptureEchoVoiceWithoutPlaybackMatch'));
    assert(log.includes('playback reached VAD: residual RMS'));
    assert(log.includes("mixed output is not the user's voice"));
    const replayed = source => source.split(/(?=^func )/m).filter(part => /^func (?:echo\w+|TestCaptureEcho\w+)\(/.test(part)).map(part => part.trim());
    assert.deepEqual(replayed(git('show', run.headSha + ':internal/audio/echo_test.go')), replayed(readFileSync('internal/audio/echo_test.go', 'utf8')));
  } else {
    assert.equal(run.conclusion, 'success');
    const job = run.jobs.find(job => job.name === 'echo-green');
    assert.equal(job?.conclusion, 'success');
    for (const step of ['Run node scripts/check-echo-scope.mjs', 'Run go vet -unsafeptr=false -lostcancel=false ./...', 'Run go test -v ./...', 'Run go test -race ./internal/audio', 'Build Windows GUI']) assert.equal(job.steps.find(candidate => candidate.name === step)?.conclusion, 'success', step);
    assert.equal(run.jobs.find(job => job.name === 'baseline-regression-control')?.conclusion, 'success');
    assert(log.includes('BASELINE REGRESSION CONTROL VERIFIED'));
    for (const test of [...regressions, 'TestCaptureEchoVoiceWithoutPlaybackMatch', 'TestEchoReferenceUnavailable', 'TestEchoReferenceBounds', 'TestEchoFFTCorrelationMatchesDirect', 'TestEchoStereoReferenceChange', 'TestEchoQuantizationHistory']) assert(log.includes('--- PASS: ' + test));
    assert(log.includes('ECHO SCOPE VERIFIED'));
    assert(log.includes('BenchmarkCaptureEcho'));
    assert(log.includes('Artifact go-dictate-1.8.139.zip successfully finalized'));
    assert.equal(git('diff', '--name-only', run.headSha, 'HEAD', '--', '.', ':!GATES.md'), '');
  }
  console.log('ECHO ' + mode.toUpperCase() + ' VERIFIED ' + run.url + ' ' + run.headSha);
} else if (mode === 'delivery') {
  assert.equal(git('branch', '--show-current'), branch);
  assert.equal(git('merge-base', 'HEAD', 'v1.8.138'), git('rev-parse', 'v1.8.138^{commit}'));
  assert.equal(git('ls-remote', 'origin', 'refs/heads/' + branch).split(/\s/)[0], git('rev-parse', 'HEAD'));
  const changed = git('status', '--porcelain').split('\n').filter(Boolean);
  assert(changed.every(line => line.endsWith('GATES.md')), 'uncommitted deliverable changes');
  assert.match(readFileSync('version.go', 'utf8'), /Version\s*= "1\.8\.139"/);
  assert.match(readFileSync('CHANGELOG.md', 'utf8'), /## \[1\.8\.139\]/);
  console.log('ECHO DELIVERY VERIFIED ' + git('rev-parse', 'HEAD'));
} else if (mode === 'pr') {
  const pr = JSON.parse(gh('pr', 'view', id, '--repo', repo, '--json', 'title,baseRefName,headRefName,state,body,url,headRefOid'));
  assert.equal(pr.state, 'OPEN');
  assert.equal(pr.title, title);
  assert.equal(pr.headRefName, branch);
  assert.equal(pr.baseRefName, 'godictate-1.8.123');
  assert.equal(pr.headRefOid, git('rev-parse', 'HEAD'));
  for (const required of ['v1.8.89', 'v1.8.90', 'v1.8.91', 'v1.8.92', 'v1.8.93', 'v1.8.94', 'v1.8.129', 'Red', 'Green', 'synthesized', 'not claimed live']) assert(pr.body.includes(required), 'missing PR evidence: ' + required);
  const links = [...pr.body.matchAll(/actions\/runs\/(\d+)/g)].map(match => match[1]);
  assert(new Set(links).size >= 2, 'red and green links missing');
  console.log('ECHO PR VERIFIED ' + pr.url);
} else {
  throw new Error('Expected red <run>, green <run>, delivery, or pr <number>');
}
