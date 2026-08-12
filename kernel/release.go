package kernel

// ReleaseState is the contract-level state of a deterministic release plan.
// Provider execution remains outside the kernel; this model only adjudicates
// whether a proposed durable transition is legal.
type ReleaseState string

const (
	ReleaseAbsent             ReleaseState = "ABSENT"
	ReleasePlanned            ReleaseState = "PLANNED"
	ReleaseQualified          ReleaseState = "QUALIFIED"
	ReleaseExecuting          ReleaseState = "EXECUTING"
	ReleaseReconciling        ReleaseState = "RECONCILING"
	ReleaseReadyForAcceptance ReleaseState = "READY_FOR_ACCEPTANCE"
	ReleaseFailed             ReleaseState = "FAILED"
	ReleaseBlocked            ReleaseState = "BLOCKED"
	ReleaseNotRequired        ReleaseState = "NO_RELEASE_REQUIRED"
)

type ReleaseMode string

const (
	ReleaseModeCode        ReleaseMode = "CODE"
	ReleaseModeNotRequired ReleaseMode = "NO_RELEASE_REQUIRED"
)

type ReleaseAction string

const (
	ReleaseCreate           ReleaseAction = "CREATE"
	ReleaseQualify          ReleaseAction = "QUALIFY"
	ReleaseRequestExecution ReleaseAction = "REQUEST_EXECUTION"
	ReleaseRecordResult     ReleaseAction = "RECORD_RESULT"
	ReleaseReconcile        ReleaseAction = "RECONCILE"
	ReleaseFinalize         ReleaseAction = "FINALIZE"
)

type ReleaseOutcome string

const (
	ReleaseOutcomeMerged        ReleaseOutcome = "MERGED"
	ReleaseOutcomeAlreadyMerged ReleaseOutcome = "ALREADY_MERGED"
	ReleaseOutcomeFailed        ReleaseOutcome = "FAILED"
	ReleaseOutcomeUnknown       ReleaseOutcome = "UNKNOWN"
)

type ReleaseSnapshot struct {
	State                 ReleaseState
	ReleaseMode           ReleaseMode
	Author                PrincipalRef
	AuthorApprovalEventID UUIDv7
	PlanDigest            Digest
	BaseCommit            string
	HeadCommits           []string
	MergeOrder            []UUIDv7
	NextMergeIndex        uint64
	ExecutionRoundLimit   uint64
	NextRound             uint64
	ActiveRound           uint64
	ActiveMergeID         UUIDv7
	ActiveAttemptID       UUIDv7
	UnresolvedAttemptID   UUIDv7
	LatestResultEventID   UUIDv7
	QualifiedTreeDigest   string
	ProviderTreeDigest    string
}

type ReleaseTransition struct {
	Action                  ReleaseAction
	Author                  PrincipalRef
	AuthorApprovalEventID   UUIDv7
	PlanDigest              Digest
	BaseCommit              string
	HeadCommits             []string
	MergeID                 UUIDv7
	AttemptID               UUIDv7
	Round                   uint64
	ResultEventID           UUIDv7
	SupersedesResultEventID UUIDv7
	Outcome                 ReleaseOutcome
	QualifiedTreeDigest     string
	ProviderTreeDigest      string
	TerminalStatus          ReleaseState
}

type ReleaseEvaluation struct {
	Accepted bool         `json:"accepted"`
	Reason   string       `json:"reason"`
	State    ReleaseState `json:"state"`
}

// EvaluateRelease is the provider-neutral executable model carried by the
// 0.5.0 conformance corpus. It never performs Git or provider I/O.
func EvaluateRelease(current ReleaseSnapshot, transition ReleaseTransition) ReleaseEvaluation {
	reject := func(reason string) ReleaseEvaluation {
		return ReleaseEvaluation{Accepted: false, Reason: reason, State: current.State}
	}
	accept := func(state ReleaseState) ReleaseEvaluation {
		return ReleaseEvaluation{Accepted: true, Reason: "ACCEPTED", State: state}
	}

	switch current.State {
	case ReleaseReadyForAcceptance, ReleaseFailed, ReleaseBlocked, ReleaseNotRequired:
		return reject("RELEASE_TERMINAL")
	}
	if transition.PlanDigest != "" && transition.PlanDigest != current.PlanDigest {
		return reject("PLAN_DIGEST_MISMATCH")
	}

	switch transition.Action {
	case ReleaseCreate:
		if current.State != ReleaseAbsent {
			return reject("INVALID_RELEASE_STATE")
		}
		if transition.Author != current.Author || transition.AuthorApprovalEventID != current.AuthorApprovalEventID {
			return reject("AUTHOR_APPROVAL_MISMATCH")
		}
		return accept(ReleasePlanned)
	case ReleaseQualify:
		if current.ReleaseMode != ReleaseModeCode || current.State != ReleasePlanned {
			return reject("INVALID_RELEASE_STATE")
		}
		if transition.BaseCommit != current.BaseCommit || !equalStrings(transition.HeadCommits, current.HeadCommits) {
			return reject("QUALIFICATION_INPUT_MISMATCH")
		}
		return accept(ReleaseQualified)
	case ReleaseRequestExecution:
		if current.State == ReleaseReconciling {
			return reject("RECONCILIATION_REQUIRED")
		}
		if current.State == ReleaseExecuting {
			return reject("EXECUTION_IN_PROGRESS")
		}
		if current.State != ReleaseQualified {
			return reject("QUALIFICATION_REQUIRED")
		}
		if current.NextMergeIndex >= uint64(len(current.MergeOrder)) || current.MergeOrder[current.NextMergeIndex] != transition.MergeID {
			return reject("MERGE_ORDER_CONFLICT")
		}
		if current.NextRound > current.ExecutionRoundLimit || transition.Round > current.ExecutionRoundLimit {
			return reject("RETRY_BUDGET_EXHAUSTED")
		}
		if transition.Round != current.NextRound {
			return reject("ROUND_CONFLICT")
		}
		return accept(ReleaseExecuting)
	case ReleaseRecordResult:
		if current.State != ReleaseExecuting || current.ActiveAttemptID != transition.AttemptID {
			return reject("ATTEMPT_MISMATCH")
		}
		return releaseOutcomeEvaluation(current, transition.Outcome)
	case ReleaseReconcile:
		if current.State != ReleaseReconciling || current.UnresolvedAttemptID != transition.AttemptID {
			return reject("ATTEMPT_MISMATCH")
		}
		if current.LatestResultEventID != transition.SupersedesResultEventID {
			return reject("RESULT_SUPERSESSION_MISMATCH")
		}
		return releaseOutcomeEvaluation(current, transition.Outcome)
	case ReleaseFinalize:
		if current.ReleaseMode == ReleaseModeNotRequired {
			if current.State == ReleasePlanned && transition.TerminalStatus == ReleaseNotRequired {
				return accept(ReleaseNotRequired)
			}
			return reject("INVALID_RELEASE_STATE")
		}
		if current.State != ReleaseQualified || current.NextMergeIndex != uint64(len(current.MergeOrder)) {
			return reject("RELEASE_INCOMPLETE")
		}
		if transition.ProviderTreeDigest != current.QualifiedTreeDigest {
			return reject("TREE_MISMATCH")
		}
		return accept(transition.TerminalStatus)
	default:
		return reject("INVALID_ACTION")
	}
}

func releaseOutcomeEvaluation(current ReleaseSnapshot, outcome ReleaseOutcome) ReleaseEvaluation {
	switch outcome {
	case ReleaseOutcomeUnknown:
		return ReleaseEvaluation{Accepted: true, Reason: "ACCEPTED", State: ReleaseReconciling}
	case ReleaseOutcomeMerged, ReleaseOutcomeAlreadyMerged:
		return ReleaseEvaluation{Accepted: true, Reason: "ACCEPTED", State: ReleaseQualified}
	case ReleaseOutcomeFailed:
		state := ReleaseQualified
		if current.ActiveRound >= current.ExecutionRoundLimit {
			state = ReleaseFailed
		}
		return ReleaseEvaluation{Accepted: true, Reason: "ACCEPTED", State: state}
	default:
		return ReleaseEvaluation{Accepted: false, Reason: "INVALID_OUTCOME", State: current.State}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
