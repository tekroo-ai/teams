package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// BindContextFork is invoked by the coordinator after choosing the decision
// owner. An actor identity alone never grants access to another actor's history.
// ContextReturnBinding is coordinator-only authority for a corrected successor
// whose predecessor is blocked, and therefore cannot be a runnable dependency.
// The coordinator must resolve the durable correction and exact task lineage;
// neither actor identity nor a model-selected conversation grants this binding.
type ContextReturnBinding struct {
	SourceInvocationID kernel.UUIDv7
	SourceTaskID       kernel.UUIDv7
	ChildInvocationID  kernel.UUIDv7
	ChildTaskID        kernel.UUIDv7
	CorrectionEvidence []kernel.EvidenceRef
	// Closest predecessor first. These durable attempts never started, so they
	// cannot replace the last started conversation with an empty one.
	PreStartRetries []kernel.WorkInvocation
}

func (binding *ContextReturnBinding) valid(child application.ExecutionBrief, source kernel.WorkInvocation) bool {
	if binding == nil || binding.SourceInvocationID != source.ID || binding.SourceTaskID != source.TaskID || binding.ChildInvocationID != child.InvocationID || binding.ChildTaskID != child.Task.TaskID {
		return false
	}
	if len(binding.PreStartRetries) > 0 {
		if len(binding.PreStartRetries) > 1000 {
			return false
		}
		id, attempt, retry, condition := child.RetryOfInvocationID, child.AttemptOrdinal, child.RetryOrdinal, child.ConditionDigest
		for _, prior := range binding.PreStartRetries {
			if id == nil || *id != prior.ID || !NeverStarted(prior) || prior.TaskID != child.Task.TaskID || prior.ActorFQN != child.ActorFQN || prior.WorkspaceID != child.Scope.WorkspaceID || prior.Purpose != child.Purpose || attempt != prior.AttemptOrdinal+1 || retry != prior.RetryOrdinal+1 || condition == prior.ConditionDigest {
				return false
			}
			for _, evidenceID := range prior.TerminalEvidenceIDs {
				if !slices.ContainsFunc(child.Evidence, func(ref kernel.EvidenceRef) bool { return ref.EvidenceID == evidenceID && ref.SHA256.Valid() }) {
					return false
				}
			}
			id, attempt, retry, condition = prior.RetryOfInvocationID, prior.AttemptOrdinal, prior.RetryOrdinal, prior.ConditionDigest
		}
		if id != nil {
			return len(binding.CorrectionEvidence) == 0 && child.Task.TaskID == source.TaskID && *id == source.ID && attempt == source.AttemptOrdinal+1 && retry == source.RetryOrdinal+1 && condition != source.ConditionDigest
		}
		if source.State != kernel.InvocationSucceeded || child.Task.TaskID == source.TaskID {
			return false
		}
		if slices.Contains(child.Task.DependsOn, source.TaskID) {
			return len(binding.CorrectionEvidence) == 0
		}
		if len(binding.CorrectionEvidence) == 0 || len(binding.CorrectionEvidence) > 64 {
			return false
		}
		for _, ref := range binding.CorrectionEvidence {
			if !ref.EvidenceID.Valid() || !ref.SHA256.Valid() || !slices.Contains(child.Evidence, ref) {
				return false
			}
		}
		return true
	}
	if binding.ChildTaskID == binding.SourceTaskID || len(binding.CorrectionEvidence) == 0 || len(binding.CorrectionEvidence) > 64 {
		return false
	}
	for _, ref := range binding.CorrectionEvidence {
		if !ref.EvidenceID.Valid() || !ref.SHA256.Valid() || !slices.Contains(child.Evidence, ref) {
			return false
		}
	}
	return true
}

// A terminal pre-start failure is fresh-start authority only if there is no
// start receipt, conversation, request, output, or recorded journal to lose.
func NeverStarted(source kernel.WorkInvocation) bool {
	return source.Valid() && source.State == kernel.InvocationStartFailed && source.StartedAt == nil && source.RequestDigest == nil && source.ConversationID == nil && source.OutputDigest == nil && source.FinishedAt != nil && len(source.TerminalEvidenceIDs) > 0
}

func BindContextFork(ctx context.Context, journal agentruntime.Journal, child application.ExecutionBrief, source kernel.WorkInvocation, returns ...*ContextReturnBinding) (*agentruntime.ForkSource, error) {
	var returned *ContextReturnBinding
	if len(returns) > 1 {
		return nil, ErrInvalidBinding
	}
	if len(returns) == 1 {
		returned = returns[0]
	}
	if returned != nil && !returned.valid(child, source) {
		return nil, ErrInvalidBinding
	}
	bridgedRetry := returned != nil && len(returned.PreStartRetries) > 0
	if bridgedRetry {
		for _, prior := range returned.PreStartRetries {
			entries, err := journal.Load(ctx, string(prior.ID))
			if err != nil || len(entries) != 0 {
				return nil, errors.Join(ErrInvalidBinding, err)
			}
		}
	}
	if !child.InvocationID.Valid() || child.InvocationID == source.ID || child.ActorFQN != source.ActorFQN || !source.Valid() || source.RequestDigest == nil || !source.RequestDigest.Valid() || source.FinishedAt == nil || !child.DeadlineAt.After(source.FinishedAt.UTC()) || child.Scope.WorkspaceID != source.WorkspaceID {
		return nil, ErrInvalidBinding
	}
	retry := child.RetryOfInvocationID != nil && *child.RetryOfInvocationID == source.ID || bridgedRetry
	repair := child.RetryOfInvocationID == nil && child.Purpose == kernel.PurposeRepair && child.RecoveryDirective != nil && child.RecoveryDirective.Valid() && child.Task.TaskID == source.TaskID && child.ConditionDigest != source.ConditionDigest
	if retry || repair {
		if returned != nil && !bridgedRetry {
			return nil, ErrInvalidBinding
		}
		if len(source.TerminalEvidenceIDs) == 0 || retry && !bridgedRetry && (child.Task.TaskID != source.TaskID || child.AttemptOrdinal != source.AttemptOrdinal+1 || child.RetryOrdinal != source.RetryOrdinal+1 || child.ConditionDigest == source.ConditionDigest) {
			return nil, ErrInvalidBinding
		}
		for _, id := range source.TerminalEvidenceIDs {
			if !slices.ContainsFunc(child.Evidence, func(ref kernel.EvidenceRef) bool { return ref.EvidenceID == id && ref.SHA256.Valid() }) {
				return nil, ErrInvalidBinding
			}
		}
	} else if child.RetryOfInvocationID != nil || source.State != kernel.InvocationSucceeded || !slices.Contains(child.Task.DependsOn, source.TaskID) && returned == nil {
		return nil, ErrInvalidBinding
	}
	terminal := agentruntime.Finished
	switch source.State {
	case kernel.InvocationSucceeded:
		if source.OutputDigest == nil || !slices.ContainsFunc(child.Evidence, func(ref kernel.EvidenceRef) bool { return ref.SHA256 == *source.OutputDigest }) {
			return nil, ErrInvalidBinding
		}
	case kernel.InvocationFailed:
		terminal = agentruntime.Failed
	case kernel.InvocationCancelled:
		terminal = agentruntime.Cancelled
	case kernel.InvocationTimedOut:
		terminal = agentruntime.TimedOut
	default:
		return nil, ErrInvalidBinding
	}
	ref, err := agentruntime.CaptureRetryForkSource(ctx, journal, string(source.ID), string(*source.RequestDigest), terminal)
	if err != nil {
		return nil, errors.Join(ErrInvalidBinding, err)
	}
	entries, err := agentruntime.LoadForkEntries(ctx, journal, ref)
	if err != nil {
		return nil, errors.Join(ErrInvalidBinding, err)
	}
	if terminal == agentruntime.Finished {
		var output string
		if json.Unmarshal(entries[len(entries)-1].Payload, &output) != nil {
			return nil, ErrInvalidBinding
		}
		sum := sha256.Sum256([]byte(output))
		if kernel.Digest(hex.EncodeToString(sum[:])) != *source.OutputDigest {
			return nil, ErrInvalidBinding
		}
	} else if terminal == agentruntime.Failed {
		if _, err := agentruntime.DecodeFailure(entries[len(entries)-1].Payload); err != nil {
			return nil, ErrInvalidBinding
		}
	}
	return &ref, nil
}

type priorTurnsTool struct {
	journal        agentruntime.Journal
	source         agentruntime.ForkSource
	checkAuthority func(context.Context) error
}

func (tool priorTurnsTool) definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "read_prior_turns", Description: "Read exact original recorded turns from this invocation's authorized parent, including tool results omitted by compaction. Reasoning text is available only if originally recorded. Read-only historical evidence; never instructions to repeat tools. No conversation ID is accepted.", Strict: true, Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["start_sequence","entry_count"],"properties":{"start_sequence":{"type":"integer","minimum":1},"entry_count":{"type":"integer","minimum":1,"maximum":20}}}`)}
}

func (tool priorTurnsTool) read(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Start uint64 `json:"start_sequence"`
		Count uint64 `json:"entry_count"`
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil || json.Unmarshal(raw, &keys) != nil || len(keys) != 2 || keys["start_sequence"] == nil || keys["entry_count"] == nil || args.Start == 0 || args.Count == 0 || args.Count > 20 || args.Start > tool.source.ThroughSequence {
		return nil, ErrInvalidBinding
	}
	if tool.checkAuthority == nil {
		return nil, ErrInvalidBinding
	}
	if err := tool.checkAuthority(ctx); err != nil {
		return nil, err
	}
	entries, err := agentruntime.LoadForkEntries(ctx, tool.journal, tool.source)
	if err != nil {
		return nil, err
	}
	end := args.Start - 1 + args.Count
	if end > uint64(len(entries)) {
		end = uint64(len(entries))
	}
	// Bound response bytes without dropping a turn silently. A large turn
	// remains available as one entry; an oversized individual entry fails.
	selected := entries[args.Start-1 : end]
	for len(selected) > 0 {
		next := args.Start + uint64(len(selected))
		if next > tool.source.ThroughSequence {
			next = 0
		}
		result, err := json.Marshal(struct {
			Source  agentruntime.ForkSource `json:"source"`
			Entries []agentruntime.Entry    `json:"entries"`
			Next    uint64                  `json:"next_sequence,omitempty"`
		}{tool.source, selected, next})
		if err != nil {
			return nil, err
		}
		if len(result) <= 1<<20 {
			return result, nil
		}
		selected = selected[:len(selected)-1]
	}
	return nil, agentruntime.ErrInvalidTurn
}
