# SMA E1 ddalcu candidate 1 offline readiness

Status: `PASS_OFFLINE / NOT_EXECUTION_AUTHORITY`

## Outcome

OBSERVED: E1 candidate 1 binds the accepted S1, S2, and M1 claims to the exact
`ddalcu--Qwen3.8-27B-MLX-Serve-8bit` profile at
`http://127.0.0.1:8802/v1`, with thinking disabled, temperature zero, no
tools, 128 maximum output tokens, and a 120-second request timeout.

OBSERVED: the candidate preserves all 18 historical `SMAQ1N-*` scenario
prompts and all 96 repetitions byte-for-byte from
`preregistration-native-v2.json`. It reuses the accepted S2 raw-receipt
orchestration for SMA and OpenHands and adds a transparent localhost model
audit proxy. The audit proxy forwards to the accepted M1 endpoint and retains
content-addressed request and response evidence without modifying prompts.

COMPUTED: the normal and optimized offline walks each completed 96/96
repetitions. Every repetition produced the required component verdict vector;
all 96 overall verdicts were `PASS`. The append-before-action ledger verified,
prompt drift and thinking-enabled requests were rejected, an incorrect model
answer was attributed to the model component, and cleanup failure stopped as a
safety failure.

OBSERVED: no service was started or restarted, no OpenHands conversation was
created, no model was called, no database was contacted, and no live or
measured E1 execution occurred.

## Frozen-candidate hashes

- Corpus: `69ad47c6b73f3d3d5b326e3c1817f22f1d5aa6e6ebe91970a448ec21dd6778c2`
- Preregistration: `de9f90bdbbf5a422c2d77dc1e958ac27a09c6a6e2977a198d2cc6ca07c5fd045`
- Prepared live definition: `4cf88b032b19a5e9ab7e9e5429badb01fa0975d05506d6464954d83ff3cda514`
- Offline qualification file: `2e765e621ee9d96b8acc34ecbdb02c1a4a910d2aa4c822e534d212896296a193`
- Offline qualification embedded receipt: `d65d75d086193c78882483f536d71e0b269b5d3d57566c13be931424bc6b702e`
- Offline 96-record walk: `038921ce129f06144fd9948eec79f059724363e30d1dade37002420a08d11ad4`
- Candidate package identity file: `8756b30a06803e56dd77ce4bc114b22bd4f521bdc2360264e856f82718fa6389`
- Candidate content identity: `7c87e8d1348e92fc7ead0a590e8549e3403daf8be77db262115a04cc80e30f1b`

## Next gate

The next action is principal `ACCEPT/FREEZE` of the E1 corpus,
preregistration, candidate package identity, and offline qualification. That
acceptance authorizes preparation of a separate exact execution identity; it
does not itself authorize services, OpenHands conversations, model calls, a
live rehearsal, or the 96-repetition measured E1 execution.
