# Gates: IGSH-710 laptop echo rejection

OWNS: GATES.md, internal/audio/**, main.go, version.go, CHANGELOG.md, .github/workflows/**, scripts/**

Scope: Fix only bug 2 on v1.8.138 in ceo/IGSH-710-echo, preserving ducking and Deepgram lifecycle, with deterministic evidence and a PR against the released line. No local Go builds/tests, Gradle, Wine, board operations, new user-facing controls, or code comments.

- [ ] G0: This ledger defines falsifiable outcomes
  CHECK: node /home/valentin/.agents/skills/unlazy/scripts/gate-lint.mjs GATES.md
  EXPECT: LINT OK
  EVIDENCE: pending

- [ ] G1: History and measured mic/loopback fixtures justify the selected method
  EVIDENCE: pending

- [ ] G2: Deterministic signal regressions fail at runtime through the released capture callback in GitHub Actions
  CHECK: node scripts/verify-echo-evidence.mjs red 40133815919
  EXPECT: ECHO RED VERIFIED
  EVIDENCE: pending

- [ ] G3: GitHub Actions passes all signal regressions, full tests, race tests, vet, benchmark and Windows GUI build on the final code
  CHECK: node scripts/verify-echo-evidence.mjs green 40134303644
  EXPECT: ECHO GREEN VERIFIED
  EVIDENCE: pending

- [ ] G4: Deepgram lifecycle, VAD, media filter, configuration and ducking remain unchanged; mutation controls fail, capture filtering is wired, and no Go comments or user-facing controls were added
  CHECK: node scripts/check-echo-scope.mjs
  EXPECT: ECHO SCOPE VERIFIED
  EVIDENCE: pending

- [ ] G5: Version/changelog are correct and the release-derived branch is committed and pushed
  CHECK: node scripts/verify-echo-evidence.mjs delivery
  EXPECT: ECHO DELIVERY VERIFIED
  EVIDENCE: pending

- [ ] G6: Open PR has the requested title, released-line base, historical explanation and red/green evidence
  CHECK: node scripts/verify-echo-evidence.mjs pr 14
  EXPECT: ECHO PR VERIFIED
  EVIDENCE: pending
