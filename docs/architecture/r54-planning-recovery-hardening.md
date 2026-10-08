# r54 planning and recovery hardening

## Boundaries

Fix Teams orchestration and process contracts, not the alias feature. Preserve
r54 outputs and checkpoints. Do not weaken kernel authorization, grant advisory
nodes write authority, edit accepted CONTRACTS, or silently expand a task scope.
Deployment and live recovery remain separate from implementation qualification.

## Raw diagnosis (2026-10-07)

- Coder-4 invocation `4a3f0eea-1bd5-7145-882d-132ca4d9f14a` submitted
  `needs_decision`; raw turn entries 95-97 contain the compiler diagnostics and
  result. Block event `01a11868-3309-7e42-872b-7b9e596d1d0f` binds that result.
- Advisory task `ce9ddf4c-715f-7492-852e-4c891ebf7c7b` was created by event
  `01a11868-343d-751d-b42e-b2d38fd50e8b` at 22:07:24.733564 UTC.
- Profile-bind receipt `47526e02-00bb-7720-8d57-0fef5a890233` was
  `REJECTED_UNAUTHORIZED` at 22:07:24.760401 UTC. The consultation builder
  declares only its first classification evidence ID but submits all terminal
  evidence from both the blocked implementation and original architecture.
  Provenance `14b367b187edcc16d38eebf0abef3cb2e460c5c4ce7f827c8c5fdeb8793dff13`
  attaches four references. The admission regression now uses four, not two.
  `kernel/decision_policy.go` requires these sets to match exactly.
- The diagnostics endpoint recorded 521 repetitions of this feature-work fault
  through 22:17:55 UTC. There was no advisory model invocation.
- Tester-2's negative result is a separate team validation finding, not the cause
  of the profile-bind rejection. Its broader wiring claims require inspection
  against the actual candidate before being treated as established defects.

## Work plan

1. Bind the complete admitted consultation evidence set; give the corrected
   command an evidence-bound identity so the retained rejected receipt is not
   replayed. Add real Mongo/kernel tests reproducing the rejection, corrected
   binding, and restart/idempotence. Keep exact-match authorization intact.
2. Qualify advisory admission and retained-context completion through the actual
   service path, including unresolved scope answers and no conversation cycles.
   Isolate a durable profile rejection from unrelated terminal ingestion and
   later features without admitting repair/retry work inside the faulty feature.
3. Strengthen architecture/PM impact coverage: production implementations,
   construction/wiring, existing callers and test doubles must have owners and
   usable scope. Distinguish structural validation from model review; do not
   claim that a prose instruction proves semantic completeness.
4. Design any bounded PM scope amendment against existing accepted authority,
   evidence, budget, scope-revision and DAG rules. A clarification answer alone
   must never authorize a wider write scope. If a successor contract is needed,
   keep it separate and request its acceptance before activation.
5. Run targeted tests/race tests and ordinary suite. Record precisely what is
   implemented and qualified; do not deploy or retry live work implicitly.

## Progress

- Diagnosis of advisory profile-bind rejection: verified.
- Evidence binding and new evidence-bound command identity: implemented. Exact
  kernel matching and old rejected receipts are unchanged.
- Receipt-backed fault isolation: implemented. Known unchanged profile-bind
  rejection permits terminal-only reconciliation of unrelated work while the
  fault is still returned. The original `needs_decision` output is excluded,
  including its dependency contribution, so partial consultation recovery cannot
  re-block or prematurely complete that task. Output blobs must remain available
  and digest-correct. Unknown outcomes, changed-state receipts and unrelated
  command failures do not enter this fallback. Other features are processed
  independently; all feature faults are aggregated, and cancellation stops work.
- Terminal-only mode may retain negative reviews or complete evidence-backed
  work, but cannot authorize repairs or structured-output retries. It does not
  widen scope, declare a failed validator successful, or silence the fault.
- Isolated real Mongo/kernel regression: passed. It reproduces the old rejection,
  applies the corrected profile, closes/reopens the native service, verifies
  replay does not append another profile event, retains rejection of the old
  malformed command, and proceeds through normal assignment/scope/budget
  admission to exactly one authorized HANDOFF invocation. Its controller state
  is a fixture; no dispatch worker or model request is started. This is not a
  complete model-driven feature qualification.
- Additional regression receipts (2026-10-08):
  `TestConsultationProfileRecoversRejectedEvidenceBindingAcrossRestart` passes
  with `mongo_integration` and `-race` (16.633s package receipt), including real
  task admission/evidence/block transitions for an unrelated negative finding
  and malformed promotion output. Neither admits new model work; the original
  question head remains unchanged. Terminal invocation bindings in these two
  cases are explicitly fixture projections, not executed model invocations.
  `reconciliation_fault_isolation_test.go` passes with `-race` (2.736s), covering
  digest/unavailable output, fault classification, retained error identity,
  cross-feature progress and cancellation. The ordinary full suite passes;
  operationalruntime/nativeagent/organization/agenttools race tests pass; the
  amendment preparation/rejection integration group passes with `-race`
  (92.039s). Two fixture actor/fencing collisions were corrected before these
  passes; they were test-setup failures, not dismissed as transient behavior.
- Existing retained-context tests: passed, including original rationale in the
  child session, raw historical objects, source/actor/workspace/evidence binding,
  restart without a repeated model request, and rejection of scope-granting
  consultation output.
- Architecture/PM interface-impact guidance: implemented in the shared prompt
  builder, after the selected workflow instruction. Covers concrete production
  implementers, construction/wiring, callers, existing test doubles, ownership,
  write scope and distinguishing verification. Tests verify both stages, unchanged
  workflow prefix and valid authoritative JSON. This is model review guidance,
  not deterministic proof that every impacted site was discovered.
- Automatic in-flight plan amendment: implementation approved as successor
  candidate **0.15.0**, now staged but **not activated or fully qualified**.
  The domain/schema, one-use transactional ledger, retained-context PM path,
  independent policy check, private checkpoint, fresh successor preparation and
  separately fenced publication are implemented. Component tests pass; the
  isolated automatic coordinator/frontier round trip now passes with race
  detection, including successor admission/restart and dirty checkpoint
  preservation. Its canned model and minimal signed role fixtures do not
  qualify current production-role or live-model behavior.
  `bounded-inflight-plan-amendment-design.md` records exact receipts and gaps.
  Acceptance/deployment are separate authority; no accepted package was changed.
- The integrated test uncovered and now covers two additional harness faults:
  exact protocol-only prompt reconstruction during native journal inspection,
  and explicit blocked-checkpoint lineage validation during successor admission.
  Neither fix relaxes result schema, evidence matching or candidate acceptance.
- Latest extended integration receipt: forward test 136.17s (140.721s package),
  normal successor admitted once after publication and unchanged after restart;
  preparation/rejection/consultation integration group passes with `-race`
  (105.663s). A subsequent broad suite failure had its individual receipt
  truncated; package JSON-output rerun passes, but that failure's cause remains
  unverified. The subsequent full JSON-logged suite and five ordinary package
  repetitions pass. The final forward test also rejects a changed checkpoint
  policy binding without mutating the budget. See the amendment design's test
  caveat and retained JSON receipts.
- Verification receipts: `go test ./... -count=1 -timeout=300s` passed; targeted
  operationalruntime/nativeagent/application tests passed normally and with
  `-race`; the Mongo-tagged admission regression passed normally (14.505s) and
  with `-race` (14.813s); `git diff --check` passed.
- During implementation qualification, no commit, deployment, team retry,
  model steering, feature edit or live-state mutation was performed. r54
  preservation remains required.
- Review reproduced and repaired historical journal inspection after plan
  publication: both consultation and assessment now resolve their retained prior
  plan and exact invocation, including terminal feature states. The forward
  race test passes (137.21s), rejects changed invocation bindings and checks the
  used amendment allowance without another assessment. The separate in-scope
  race test passes (16.60s), authorizing the existing-workspace continuation
  without a PM call or scope expansion. Two fixture setup/replay errors were
  diagnosed and corrected; production authorization was not changed. The
  logged full ordinary suite passes all 31 packages. Exact receipts and the
  bounded lookup limitation are recorded in the amendment design.
- The organizational 0.15 candidate can now be frozen for exact principal
  policy acceptance. It is not a replacement for the immutable 0.12 kernel
  command catalogue, and freezing alone does not activate amendment authority.
