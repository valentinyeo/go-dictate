# Gates: IGSH-710 laptop echo rejection

OWNS: GATES.md, internal/audio/**, main.go, version.go, CHANGELOG.md, .github/workflows/**, scripts/**

Scope: Fix only bug 2 on v1.8.138 in ceo/IGSH-710-echo, preserving ducking and Deepgram lifecycle, with deterministic evidence and a PR against the released line. No local Go builds/tests, Gradle, Wine, board operations, new user-facing controls, or code comments.

- [x] G0: This ledger defines falsifiable outcomes
  CHECK: node /home/valentin/.agents/skills/unlazy/scripts/gate-lint.mjs GATES.md
  EXPECT: LINT OK
  EVIDENCE: automatic-evidence=v1; definition-sha256=69f81179f3934636347ff01de4824d2a1f500f2d29238b9fd688f82f77d1c57b; exit=0; EXPECT=matched; output-sha256=651d376da9258c7361dab6eab597e455ed92b643c016b570616cfda581c35ce9; output-bytes=150; shell=/bin/sh; cwd=/tmp/r5-IGSH-710/wt; path=433695eed2f1/23 entries

- [x] G1: History and measured mic/loopback fixtures justify the selected method
  EVIDENCE: Reviewed 4f02688, 6a35b3e, 8a6a47d, 94638f4, 5947133, 18cbb80 and 4231352 before implementation; PR 14 explains threshold/cooldown/onset-only failures and the separate v129 ancestry. Final GitHub run 40134766874 measures playback residual <=0.000013, direct simultaneous voice gain 0.9949-0.9958, colored-room playback 0.038352 -> 0.000006 and weak-playback double-talk error 0.000608. Intermediate measurement found sparse-only colored-echo residual 0.00957 and a false residual-path correlation 0.15479, motivating fitted responses and warm-model double-talk protection, not a higher VAD threshold. Fixtures are synthetic; no live laptop or recognizer QA is claimed. Session command audit: no board operations or local Go builds/tests, Gradle or Wine; all writes and commits were in the fresh release-derived worktree.

- [x] G2: Deterministic signal regressions fail at runtime through the released capture callback in GitHub Actions
  CHECK: node scripts/verify-echo-evidence.mjs red 40133815919
  EXPECT: ECHO RED VERIFIED
  EVIDENCE: automatic-evidence=v1; definition-sha256=4a6b7c38096cb56cac5752b412f7991c39b79c67a087d69d1547d2d8e071b517; exit=0; EXPECT=matched; output-sha256=bdadff9a33bdf923d33ce666fa1471b07b0bccecf2a3d1d581ace3025df78c50; output-bytes=126; shell=/bin/sh; cwd=/tmp/r5-IGSH-710/wt; path=433695eed2f1/23 entries

- [x] G3: GitHub Actions passes all signal regressions, full tests, race tests, vet, benchmark and Windows GUI build on the final code
  CHECK: node scripts/verify-echo-evidence.mjs green 40134766874
  EXPECT: ECHO GREEN VERIFIED
  EVIDENCE: automatic-evidence=v1; definition-sha256=8df53bd7a610441d841ace80629597abe06155254f253cacd431636258bebcb2; exit=0; EXPECT=matched; output-sha256=0f537ce505d903782354b8e06078a51515cf25624425e5adec4f4dffe2ad9ea3; output-bytes=128; shell=/bin/sh; cwd=/tmp/r5-IGSH-710/wt; path=433695eed2f1/23 entries

- [x] G4: Deepgram lifecycle, VAD, media filter, configuration and ducking remain unchanged; mutation controls fail, capture filtering is wired, and no Go comments or user-facing controls were added
  CHECK: node scripts/check-echo-scope.mjs
  EXPECT: ECHO SCOPE VERIFIED
  EVIDENCE: automatic-evidence=v1; definition-sha256=226dbae5945b8323217d84dcd2ac6afcd4a1cbc2b65ac9979e60096c722858fb; exit=0; EXPECT=matched; output-sha256=801e9eaadf922732a4d5e194dd2e8f05257bd5577cb1b1ea98f2ceeea79271d9; output-bytes=151; shell=/bin/sh; cwd=/tmp/r5-IGSH-710/wt; path=433695eed2f1/23 entries

- [x] G5: Version/changelog are correct and the release-derived branch is committed and pushed
  CHECK: node scripts/verify-echo-evidence.mjs delivery
  EXPECT: ECHO DELIVERY VERIFIED
  EVIDENCE: automatic-evidence=v1; definition-sha256=6607f0d89c8265171f151ca24157246802236445fa134883c5491416e35aa877; exit=0; EXPECT=matched; output-sha256=f523ba82fd5ecaa414662828a057cb85f769707a0f31e149e13a631d036d8a21; output-bytes=64; shell=/bin/sh; cwd=/tmp/r5-IGSH-710/wt; path=433695eed2f1/23 entries

- [x] G6: Open PR has the requested title, released-line base, historical explanation and red/green evidence
  CHECK: node scripts/verify-echo-evidence.mjs pr 14
  EXPECT: ECHO PR VERIFIED
  EVIDENCE: automatic-evidence=v1; definition-sha256=02356c5e69969b965547e38795afd4f26149eccec039d8f9056ff34d11f885fe; exit=0; EXPECT=matched; output-sha256=e8657b53916b57de7e6b93acc73e3736d5062cbf3eaafce2e37cc70f6f2775e6; output-bytes=67; shell=/bin/sh; cwd=/tmp/r5-IGSH-710/wt; path=433695eed2f1/23 entries
