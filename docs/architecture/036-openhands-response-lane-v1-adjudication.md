# OpenHands response-lane V1 adjudication

The independent response-only lane is **FAIL / NO-GO**. Do not execute another SMA-Q1 attempt yet.

## What the test established

- **OBSERVED:** Offline artifact validation and live preflight passed.
- **OBSERVED:** No SMA service, SMA bridge, hook, environment tool, sub-agent, MCP server, or model-switching tool participated.
- **OBSERVED:** OpenHands created the first fresh conversation, loaded zero tools from the explicit tool specification, loaded the pinned `qwen3.6-fast` profile, and accepted the prompt.
- **OBSERVED:** No agent message or `ConversationErrorEvent` appeared within 120 seconds. The frozen test therefore failed on conversation 1 of 128 and stopped.
- **OBSERVED:** The conversation was deleted and a subsequent GET returned 404; the disposable workspace is absent.
- **OBSERVED AFTER CLEANUP:** A direct Ollama control returned the exact requested response in approximately 0.87 seconds.
- **INFERRED:** The Step 15 blocker is in the OpenHands/model evaluation lane, not SMA retrieval or memory behavior.
- **UNVERIFIED:** The available receipts do not distinguish a queued or long-running model generation from an OpenHands scheduling or terminalization problem.

## Recommendation

Do not modify the shared `qwen3.6-fast` profile and do not begin another SMA-Q1 manifest cycle. Create a dedicated response-canary profile with a bounded LLM timeout, a small output-token ceiling, zero retries, thinking disabled, and the same bare-agent restrictions. Enhance the canary to retain content-free event/status and request-timing evidence before deletion. A separately accepted and authorized qualification must then pass at least 128 consecutive fresh conversations before another SMA-Q1 execution is considered.
