package kernel

type LifecycleAction string

const (
	ActionAuthorize     LifecycleAction = "authorize"
	ActionBeginPlanning LifecycleAction = "begin_planning"
	ActionMarkReady     LifecycleAction = "mark_ready"
	ActionActivate      LifecycleAction = "activate"
	ActionComplete      LifecycleAction = "complete"
	ActionAccept        LifecycleAction = "accept"
	ActionBlock         LifecycleAction = "block"
	ActionUnblock       LifecycleAction = "unblock"
	ActionReopen        LifecycleAction = "reopen"
	ActionClose         LifecycleAction = "close"
)

const ReasonRejectedPolicy = "REJECTED_POLICY"

type LifecycleModel string

const (
	LifecycleStory LifecycleModel = "story"
	LifecycleTask  LifecycleModel = "task"
)

type LifecycleState struct {
	Phase          Phase     `json:"phase"`
	Condition      Condition `json:"condition"`
	LifecycleEpoch uint64    `json:"lifecycleEpoch"`
	Rejected       *string   `json:"rejected"`
}

func ApplyLifecycleActions(model LifecycleModel, initial LifecycleState, actions []LifecycleAction) LifecycleState {
	state := initial
	state.Rejected = nil

	for _, action := range actions {
		if !applyLifecycleAction(model, &state, action) {
			reason := ReasonRejectedPolicy
			state.Rejected = &reason
			return state
		}
	}
	return state
}

func applyLifecycleAction(model LifecycleModel, state *LifecycleState, action LifecycleAction) bool {
	switch action {
	case ActionBlock:
		if state.Condition != ConditionRunnable || state.Phase == PhaseClosed {
			return false
		}
		state.Condition = ConditionBlocked
		return true
	case ActionUnblock:
		if state.Condition != ConditionBlocked || state.Phase == PhaseClosed {
			return false
		}
		state.Condition = ConditionRunnable
		return true
	case ActionReopen:
		if model == LifecycleStory && (state.Phase == PhaseCompleted || state.Phase == PhaseAccepted) {
			state.Phase = PhaseActive
			state.LifecycleEpoch++
			return true
		}
		if model == LifecycleTask && state.Phase == PhaseCompleted {
			state.Phase = PhaseActive
			state.LifecycleEpoch++
			return true
		}
		return false
	case ActionClose:
		if state.Phase == PhaseClosed {
			return false
		}
		state.Phase = PhaseClosed
		return true
	}

	next, ok := lifecycleTransition(model, state.Phase, action)
	if !ok {
		return false
	}
	state.Phase = next
	return true
}

func lifecycleTransition(model LifecycleModel, phase Phase, action LifecycleAction) (Phase, bool) {
	if model == LifecycleStory {
		switch {
		case phase == PhaseDraft && action == ActionAuthorize:
			return PhaseReady, true
		case phase == PhaseReady && action == ActionBeginPlanning:
			return PhasePlanning, true
		case phase == PhasePlanning && action == ActionActivate:
			return PhaseActive, true
		case phase == PhaseActive && action == ActionComplete:
			return PhaseCompleted, true
		case phase == PhaseCompleted && action == ActionAccept:
			return PhaseAccepted, true
		default:
			return "", false
		}
	}

	if model == LifecycleTask {
		switch {
		case phase == PhasePlanned && action == ActionMarkReady:
			return PhaseReady, true
		case phase == PhaseReady && action == ActionActivate:
			return PhaseActive, true
		case phase == PhaseActive && action == ActionComplete:
			return PhaseCompleted, true
		default:
			return "", false
		}
	}
	return "", false
}
