# Gates: IGSH-710 laptop echo rejection

OWNS: GATES.md, internal/audio/**, main.go, version.go, CHANGELOG.md, .github/workflows/**, scripts/**

Scope: Fix only bug 2 on v1.8.138 in ceo/IGSH-710-echo, preserving ducking and Deepgram lifecycle, with deterministic evidence and a PR against the released line. No local Go builds/tests, Gradle, Wine, board operations, new user-facing controls, or code comments.

- [ ] G0: This ledger defines falsifiable outcomes
  CHECK: node /home/valentin/.agents/skills/unlazy/scripts/gate-lint.mjs GATES.md
  EXPECT: LINT OK
  EVIDENCE: pending

- [ ] G1: History and measured mic/loopback fixtures justify the selected method
  EVIDENCE: pending

- [ ] G2: Deterministic regression tests fail on v1.8.138 in GitHub Actions for the intended echo failures
  EVIDENCE: pending

- [ ] G3: GitHub Actions passes regression tests, full supported checks and Windows build on the final code
  EVIDENCE: pending

- [ ] G4: Deepgram lifecycle and ducking remain unchanged, with a failing mutation control; no added code comments or user-facing controls
  EVIDENCE: pending

- [ ] G5: Version is v1.8.139 with changelog, and changes are committed and pushed from the requested worktree/branch
  EVIDENCE: pending

- [ ] G6: PR has requested title, released-line base, historical explanation and red/green evidence
  EVIDENCE: pending
