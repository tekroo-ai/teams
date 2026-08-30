# SMA-M1 ddalcu Qwen3.8 candidate 1 readiness

Status: **READY FOR ACCEPT/FREEZE; EXECUTION NOT AUTHORIZED**
Prepared: 2026-08-30

## Scope

This package advances Step 15 from accepted S1 and S2 into M1 model-profile
qualification. M1 is isolated from live SMA, OpenHands lifecycle, MongoDB, and
Qdrant behavior.

The exact profile is:

- endpoint: `http://127.0.0.1:8802/v1/chat/completions`
- model: `ddalcu--Qwen3.8-27B-MLX-Serve-8bit`
- runtime: `mlx-serve 26.8.10`, MLX `0.32.0`, M5 NAX enabled
- thinking: disabled with `chat_template_kwargs.enable_thinking=false`
- tools: none
- temperature: `0`
- output bound: `128` tokens
- timeout: `120000 ms`
- retries: zero
- concurrency: one

## Frozen candidate

- Package subject identity: `a1bea34d3c0b5e10bd1c6a889647e588ae8fb11e452009b8d5b646d9f4e54c12`
- Package identity record SHA-256: `117ca9b6a52802da1a7b9aeda2abb5f19cb9bb263bc741dcb5358a604d858340`
- Preregistration SHA-256: `4b52c135ea472e920faa7c1c9811ea683139e30b5dee2be44d95a4cd454737e0`
- Exact profile SHA-256: `c905951756c94cf0006b857bf13d74909db3cbb725ad703b175c07ffc1e7889e`
- Fixture source SHA-256: `99fd8ae52a861ae281b24c122c1c181e33f20fefb55fb9d8171f7ab2990f736e`
- Harness SHA-256: `8f4d0290aaf389b41a4f7568b1aedcf44041d72fd0ede677ea3fc1428a0d2af8`
- Offline qualification SHA-256: `68eb80de2d27ba5ddee1bc58ed6813617f37ffec13514d805ac2cd74495a2038`

The model identity binds all six weight shards, the model index and config,
tokenizer, vocabulary, merges, chat template, runtime executable, and launch
definition.

## Workload and checks

The prospective workload contains 8 cases, 12 sealed request fixtures, and 72
sequential model requests. It checks same-partition use, current-instruction
precedence over adversarial memory, cross-partition and raw-marker absence,
no-result abstention, secret absence, bounded-context use, and no-context
availability behavior.

The request layout is the S2-qualified OpenHands layout: the current prompt is
user-content segment 0 and SMA context, when present, is segment 1. SMA context
uses the exact untrusted-evidence preamble and complete memory-frame grammar.

Offline qualification passed in normal and optimized Python with identical
receipts. It verified the complete model package, all 72 planned operations,
all 12 request digests, 43 rejecting semantic mutations, and 7 non-gating
format controls. It made no network request or model call.

## Next action

Accept/freeze candidate 1, then separately authorize one 72-operation M1
execution. Acceptance alone does not authorize model calls. E1 remains
`NOT_RUN` until M1 has an accepted PASS result.
