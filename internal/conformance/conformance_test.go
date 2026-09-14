package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
)

const contractRoot = "CONTRACTS/tekroo.kernel.contracts/0.12.0"

type fixtureDocument struct {
	Fixtures []fixture `json:"fixtures"`
}

type fixture struct {
	FixtureID string          `json:"fixtureId"`
	Kind      string          `json:"kind"`
	Model     string          `json:"model"`
	Given     json.RawMessage `json:"given"`
	When      json.RawMessage `json:"when"`
	Then      struct {
		Expected json.RawMessage `json:"expected"`
	} `json:"then"`
}

func TestFrozenContractCorpus(t *testing.T) {
	repositoryRoot := locateRepositoryRoot(t)
	fsys := os.DirFS(repositoryRoot)
	catalogue, err := contract.Load(fsys, contractRoot)
	if err != nil {
		t.Fatalf("load frozen catalogue: %v", err)
	}

	fixtures := append(
		loadFixtures(t, filepath.Join(repositoryRoot, contractRoot, "fixtures/catalogue-coverage.json")),
		loadFixtures(t, filepath.Join(repositoryRoot, contractRoot, "fixtures/model-and-invariant-scenarios.json"))...,
	)
	if len(fixtures) != 305 {
		t.Fatalf("fixture count = %d, want 305", len(fixtures))
	}

	for _, item := range fixtures {
		item := item
		t.Run(item.FixtureID, func(t *testing.T) {
			actual := runFixture(t, catalogue, item)
			assertJSONEqual(t, item.Then.Expected, actual)
		})
	}
}

func runFixture(t *testing.T, catalogue *contract.Catalogue, item fixture) any {
	t.Helper()
	switch item.Kind {
	case "CATALOGUE_COMMAND":
		var input struct {
			CommandType string          `json:"commandType"`
			Payload     json.RawMessage `json:"payload"`
		}
		decode(t, item.When, &input)
		eventTypes, err := catalogue.ValidateFixtureCommand(input.CommandType, input.Payload)
		if err != nil {
			return struct {
				EventTypes  []string `json:"eventTypes"`
				OutcomeCode string   `json:"outcomeCode"`
			}{EventTypes: []string{}, OutcomeCode: "REJECTED_INVALID"}
		}
		return struct {
			EventTypes  []string `json:"eventTypes"`
			OutcomeCode string   `json:"outcomeCode"`
		}{EventTypes: eventTypes, OutcomeCode: "APPLIED"}
	case "STATE_MODEL":
		var given struct {
			Condition      kernel.Condition `json:"condition"`
			LifecycleEpoch uint64           `json:"lifecycleEpoch"`
			Phase          kernel.Phase     `json:"phase"`
		}
		var when struct {
			Actions []kernel.LifecycleAction `json:"actions"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		model := kernel.LifecycleStory
		if item.Model == "task" {
			model = kernel.LifecycleTask
		}
		return kernel.ApplyLifecycleActions(model, kernel.LifecycleState{
			Phase:          given.Phase,
			Condition:      given.Condition,
			LifecycleEpoch: given.LifecycleEpoch,
		}, when.Actions)
	case "DAG_MODEL":
		var given struct {
			Nodes []string `json:"nodes"`
		}
		var when struct {
			Edges []kernel.GraphEdge `json:"edges"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.ValidateDAG(given.Nodes, when.Edges)
	case "IDENTITY_MODEL":
		var when struct {
			Value string `json:"value"`
		}
		decode(t, item.When, &when)
		_, err := kernel.ParseActorFQN(when.Value)
		return struct {
			Valid bool `json:"valid"`
		}{Valid: err == nil}
	case "IDEMPOTENCY_MODEL":
		var when struct {
			Commands []struct {
				Scope           any `json:"scope"`
				SemanticRequest any `json:"semanticRequest"`
			} `json:"commands"`
		}
		decode(t, item.When, &when)
		ledger := kernel.NewIdempotencyLedger()
		receipts := make([]kernel.IdempotencyReceipt, 0, len(when.Commands))
		for _, command := range when.Commands {
			receipt, err := ledger.Apply(command.Scope, command.SemanticRequest)
			if err != nil {
				t.Fatalf("apply idempotency command: %v", err)
			}
			receipts = append(receipts, receipt)
		}
		return struct {
			DurableDecisionCount int                         `json:"durableDecisionCount"`
			Receipts             []kernel.IdempotencyReceipt `json:"receipts"`
		}{DurableDecisionCount: ledger.DurableDecisionCount(), Receipts: receipts}
	case "PRECONDITION_MODEL":
		var given struct {
			Revisions map[string]uint64 `json:"revisions"`
		}
		var when struct {
			Preconditions []struct {
				Aggregate        string `json:"aggregate"`
				ExpectedRevision uint64 `json:"expectedRevision"`
			} `json:"preconditions"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		valid := true
		for _, precondition := range when.Preconditions {
			if given.Revisions[precondition.Aggregate] != precondition.ExpectedRevision {
				valid = false
			}
		}
		return struct {
			Valid bool `json:"valid"`
		}{Valid: valid}
	case "REVIEW_JOIN_MODEL":
		var when struct {
			Results []kernel.ReviewBranchResult `json:"results"`
		}
		var given struct {
			RequiredBranchIDs []string `json:"requiredBranchIds"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.EvaluateAllPassJoin(given.RequiredBranchIDs, when.Results)
	case "BOUNDED_REVIEW_MODEL":
		var given struct {
			Finalized bool `json:"finalized"`
			Branch    struct {
				BranchID   string              `json:"branch_id"`
				Validator  kernel.PrincipalRef `json:"validator"`
				DeadlineAt time.Time           `json:"deadline_at"`
				RoundLimit uint64              `json:"round_limit"`
			} `json:"branch"`
			Adjudication struct {
				Adjudicator kernel.PrincipalRef `json:"adjudicator"`
				DeadlineAt  time.Time           `json:"deadline_at"`
				RoundLimit  uint64              `json:"round_limit"`
			} `json:"adjudication"`
		}
		var when struct {
			Authority                   kernel.PrincipalRef `json:"authority"`
			SourceRole                  string              `json:"sourceRole"`
			Round                       uint64              `json:"round"`
			Result                      string              `json:"result"`
			DecidedAt                   time.Time           `json:"decidedAt"`
			SupersedesResultEventIDs    []kernel.UUIDv7     `json:"supersedesResultEventIds"`
			ChangedConditionEvidenceIDs []kernel.UUIDv7     `json:"changedConditionEvidenceIds"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		evidenceID := kernel.UUIDv7("00000000-0000-7000-8000-000000000207")
		snapshot := kernel.CompletionReviewSnapshot{
			Branches:      map[string]kernel.ReviewBranchSpec{given.Branch.BranchID: {BranchID: given.Branch.BranchID, Validator: given.Branch.Validator, DeadlineAt: given.Branch.DeadlineAt, RoundLimit: given.Branch.RoundLimit}},
			Adjudication:  kernel.ReviewAdjudication{Adjudicator: given.Adjudication.Adjudicator, DeadlineAt: given.Adjudication.DeadlineAt, RoundLimit: given.Adjudication.RoundLimit},
			ResultRecords: map[string]kernel.ReviewBranchResult{}, KnownResultEvents: map[kernel.UUIDv7]kernel.ReviewBranchResult{},
		}
		if given.Finalized {
			snapshot.Finalization = &kernel.ReviewFinalization{EventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000698"), ReviewRevision: 1, TerminalStatus: "PASS"}
		}
		if len(when.SupersedesResultEventIDs) > 0 {
			prior := kernel.ReviewBranchResult{BranchID: given.Branch.BranchID, SourceRole: "VALIDATOR", Round: 1, Result: "FAIL", EventID: when.SupersedesResultEventIDs[0]}
			snapshot.ResultRecords[given.Branch.BranchID] = prior
			snapshot.KnownResultEvents[prior.EventID] = prior
		}
		result := kernel.ReviewBranchResult{BranchID: given.Branch.BranchID, SourceRole: when.SourceRole, Authority: when.Authority, Round: when.Round, Result: when.Result, EvidenceIDs: []kernel.UUIDv7{evidenceID}, SupersedesResultEventIDs: when.SupersedesResultEventIDs, ChangedConditionEvidenceIDs: when.ChangedConditionEvidenceIDs, EventID: kernel.UUIDv7("00000000-0000-7000-8000-000000000699"), DecidedAt: when.DecidedAt}
		reason := kernel.BoundedReviewResultReason(snapshot, result)
		accepted := reason == ""
		if accepted {
			reason = "ACCEPTED"
		}
		return struct {
			Accepted bool   `json:"accepted"`
			Reason   string `json:"reason"`
		}{Accepted: accepted, Reason: reason}
	case "ESCALATION_MODEL":
		var given struct {
			State                 kernel.EscalationState `json:"state"`
			Adjudicator           kernel.PrincipalRef    `json:"adjudicator"`
			TimeoutPolicy         kernel.PrincipalRef    `json:"timeoutPolicy"`
			SubjectLifecycleEpoch uint64                 `json:"subjectLifecycleEpoch"`
			DeadlineAt            time.Time              `json:"deadlineAt"`
			ResolutionRoundLimit  uint64                 `json:"resolutionRoundLimit"`
		}
		var when struct {
			Action                kernel.EscalationAction     `json:"action"`
			Authority             kernel.PrincipalRef         `json:"authority"`
			SourceRole            kernel.EscalationSourceRole `json:"sourceRole"`
			SubjectLifecycleEpoch uint64                      `json:"subjectLifecycleEpoch"`
			DecidedAt             time.Time                   `json:"decidedAt"`
			Round                 uint64                      `json:"round"`
			Outcome               kernel.EscalationOutcome    `json:"outcome"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.EvaluateEscalation(
			kernel.EscalationSnapshot{
				State: given.State, Adjudicator: given.Adjudicator, TimeoutPolicy: given.TimeoutPolicy,
				SubjectLifecycleEpoch: given.SubjectLifecycleEpoch, DeadlineAt: given.DeadlineAt,
				ResolutionRoundLimit: given.ResolutionRoundLimit,
			},
			kernel.EscalationTransition{
				Action: when.Action, Authority: when.Authority, SourceRole: when.SourceRole,
				SubjectLifecycleEpoch: when.SubjectLifecycleEpoch, DecidedAt: when.DecidedAt,
				Round: when.Round, Outcome: when.Outcome,
			},
		)
	case "RELEASE_MODEL":
		var given struct {
			State                 kernel.ReleaseState `json:"state"`
			ReleaseMode           kernel.ReleaseMode  `json:"releaseMode"`
			Author                kernel.PrincipalRef `json:"author"`
			AuthorApprovalEventID kernel.UUIDv7       `json:"authorApprovalEventId"`
			PlanDigest            kernel.Digest       `json:"planDigest"`
			BaseCommit            string              `json:"baseCommit"`
			HeadCommits           []string            `json:"headCommits"`
			MergeOrder            []kernel.UUIDv7     `json:"mergeOrder"`
			NextMergeIndex        uint64              `json:"nextMergeIndex"`
			ExecutionRoundLimit   uint64              `json:"executionRoundLimit"`
			NextRound             uint64              `json:"nextRound"`
			ActiveRound           uint64              `json:"activeRound"`
			ActiveMergeID         kernel.UUIDv7       `json:"activeMergeId"`
			ActiveAttemptID       kernel.UUIDv7       `json:"activeAttemptId"`
			UnresolvedAttemptID   kernel.UUIDv7       `json:"unresolvedAttemptId"`
			LatestResultEventID   kernel.UUIDv7       `json:"latestResultEventId"`
			QualifiedTreeDigest   string              `json:"qualifiedTreeDigest"`
			ProviderTreeDigest    string              `json:"providerTreeDigest"`
		}
		var when struct {
			Action                  kernel.ReleaseAction  `json:"action"`
			Author                  kernel.PrincipalRef   `json:"author"`
			AuthorApprovalEventID   kernel.UUIDv7         `json:"authorApprovalEventId"`
			PlanDigest              kernel.Digest         `json:"planDigest"`
			BaseCommit              string                `json:"baseCommit"`
			HeadCommits             []string              `json:"headCommits"`
			MergeID                 kernel.UUIDv7         `json:"mergeId"`
			AttemptID               kernel.UUIDv7         `json:"attemptId"`
			Round                   uint64                `json:"round"`
			ResultEventID           kernel.UUIDv7         `json:"resultEventId"`
			SupersedesResultEventID kernel.UUIDv7         `json:"supersedesResultEventId"`
			Outcome                 kernel.ReleaseOutcome `json:"outcome"`
			QualifiedTreeDigest     string                `json:"qualifiedTreeDigest"`
			ProviderTreeDigest      string                `json:"providerTreeDigest"`
			TerminalStatus          kernel.ReleaseState   `json:"terminalStatus"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.EvaluateRelease(
			kernel.ReleaseSnapshot{
				State: given.State, ReleaseMode: given.ReleaseMode, Author: given.Author, AuthorApprovalEventID: given.AuthorApprovalEventID, PlanDigest: given.PlanDigest,
				BaseCommit: given.BaseCommit, HeadCommits: given.HeadCommits,
				MergeOrder: given.MergeOrder, NextMergeIndex: given.NextMergeIndex,
				ExecutionRoundLimit: given.ExecutionRoundLimit, NextRound: given.NextRound, ActiveRound: given.ActiveRound,
				ActiveMergeID: given.ActiveMergeID, ActiveAttemptID: given.ActiveAttemptID,
				UnresolvedAttemptID: given.UnresolvedAttemptID, LatestResultEventID: given.LatestResultEventID,
				QualifiedTreeDigest: given.QualifiedTreeDigest, ProviderTreeDigest: given.ProviderTreeDigest,
			},
			kernel.ReleaseTransition{
				Action: when.Action, Author: when.Author, AuthorApprovalEventID: when.AuthorApprovalEventID, PlanDigest: when.PlanDigest, BaseCommit: when.BaseCommit, HeadCommits: when.HeadCommits,
				MergeID: when.MergeID, AttemptID: when.AttemptID, Round: when.Round, ResultEventID: when.ResultEventID,
				SupersedesResultEventID: when.SupersedesResultEventID, Outcome: when.Outcome,
				QualifiedTreeDigest: when.QualifiedTreeDigest, ProviderTreeDigest: when.ProviderTreeDigest,
				TerminalStatus: when.TerminalStatus,
			},
		)
	case "SUCCESSOR_SET_MODEL":
		var when struct {
			SuccessorIDs []kernel.UUIDv7 `json:"successorIds"`
		}
		decode(t, item.When, &when)
		return struct {
			Valid bool `json:"valid"`
		}{Valid: kernel.CanonicalSuccessorIDs(when.SuccessorIDs)}
	case "COMPATIBILITY_MODEL":
		var given struct {
			SourceVersion string `json:"sourceVersion"`
		}
		var when struct {
			ExactContextAvailable bool `json:"exactContextAvailable"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return struct {
			Outcome string `json:"outcome"`
		}{Outcome: kernel.CompatibilityOutcome(given.SourceVersion, when.ExactContextAvailable)}
	case "MODEL_CAPABILITY_POLICY_MODEL":
		var given struct {
			ScopeRevision         uint64                         `json:"scopeRevision"`
			ProfileScopeRevision  uint64                         `json:"profileScopeRevision"`
			RequiredRoute         kernel.DecisionRoute           `json:"requiredRoute"`
			SelectedRoute         kernel.DecisionRoute           `json:"selectedRoute"`
			QualificationStatus   kernel.QualificationStatus     `json:"qualificationStatus"`
			Revoked               bool                           `json:"revoked"`
			HardConstraintsPass   bool                           `json:"hardConstraintsPass"`
			RequiredDimensions    []kernel.IndependenceDimension `json:"requiredDimensions"`
			ProvenDimensions      []kernel.IndependenceDimension `json:"provenDimensions"`
			Classification        kernel.VariantClassification   `json:"classification"`
			SubmittedCandidateIDs []kernel.UUIDv7                `json:"submittedCandidateIds"`
		}
		var when struct {
			Action              kernel.ModelCapabilityPolicyAction `json:"action"`
			SelectedCandidateID kernel.UUIDv7                      `json:"selectedCandidateId"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.EvaluateModelCapabilityPolicy(kernel.ModelCapabilityPolicyInput{
			Action: when.Action, ScopeRevision: given.ScopeRevision, ProfileScopeRevision: given.ProfileScopeRevision,
			RequiredRoute: given.RequiredRoute, SelectedRoute: given.SelectedRoute,
			QualificationStatus: given.QualificationStatus, QualificationRevoked: given.Revoked,
			HardConstraintsPass: given.HardConstraintsPass, RequiredDimensions: given.RequiredDimensions,
			ProvenDimensions: given.ProvenDimensions, VariantClassification: given.Classification,
			SubmittedCandidateIDs: given.SubmittedCandidateIDs, SelectedCandidateID: when.SelectedCandidateID,
		})
	case "OPERATOR_SEPARATION_MODEL":
		var given struct {
			SourceKind            string                 `json:"sourceKind"`
			ClaimedKind           string                 `json:"claimedKind"`
			TargetKind            kernel.PrincipalKind   `json:"targetKind"`
			CurrentExecution      bool                   `json:"currentExecution"`
			ExplicitlyAuthorized  bool                   `json:"explicitlyAuthorized"`
			CommandAuthorityKinds []kernel.PrincipalKind `json:"commandAuthorityKinds"`
		}
		var when struct {
			Action string `json:"action"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		if when.Action == "ROUTE_HUMAN_REQUIRED" {
			return kernel.EvaluateHumanRequiredTarget(kernel.PrincipalRef{Kind: given.TargetKind, ID: "fixture-principal"})
		}
		actorAdmitted := false
		for _, kind := range given.CommandAuthorityKinds {
			actorAdmitted = actorAdmitted || kind == kernel.PrincipalActor
		}
		return kernel.EvaluateAuthorityPresentation(kernel.AuthorityPresentationInput{SourceKind: given.SourceKind, ClaimedKind: given.ClaimedKind, CurrentExecution: given.CurrentExecution, ActorCommandAdmitted: actorAdmitted, ExplicitlyAuthorized: given.ExplicitlyAuthorized})
	case "HUMAN_PARTICIPANT_MODEL":
		var given struct {
			ParticipantIDs                   []string               `json:"participantIds"`
			Active                           bool                   `json:"active"`
			RoleCurrent                      *bool                  `json:"roleCurrent"`
			ScopeMatches                     bool                   `json:"scopeMatches"`
			CommandAuthorized                bool                   `json:"commandAuthorized"`
			BindingActive                    bool                   `json:"bindingActive"`
			SubjectMatches                   bool                   `json:"subjectMatches"`
			AssuranceSufficient              bool                   `json:"assuranceSufficient"`
			ActiveAuthenticationBindingIDs   []kernel.UUIDv7        `json:"activeAuthenticationBindingIds"`
			DeliveryAuthenticationBindingIDs []kernel.UUIDv7        `json:"deliveryAuthenticationBindingIds"`
			RequestedConfidentiality         kernel.Confidentiality `json:"requestedConfidentiality"`
			RouteCeiling                     kernel.Confidentiality `json:"routeCeiling"`
		}
		var when struct {
			Action kernel.HumanParticipantPolicyAction `json:"action"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		roleCurrent := false
		if given.RoleCurrent != nil {
			roleCurrent = *given.RoleCurrent
		}
		return kernel.EvaluateHumanParticipantPolicy(kernel.HumanParticipantPolicyInput{
			Action: when.Action, ParticipantIDs: given.ParticipantIDs, Active: given.Active,
			RoleCurrent: roleCurrent, RoleCurrentSpecified: given.RoleCurrent != nil, ScopeMatches: given.ScopeMatches,
			CommandAuthorized: given.CommandAuthorized, BindingActive: given.BindingActive,
			SubjectMatches: given.SubjectMatches, AssuranceSufficient: given.AssuranceSufficient,
			ActiveAuthenticationBindingIDs:   given.ActiveAuthenticationBindingIDs,
			DeliveryAuthenticationBindingIDs: given.DeliveryAuthenticationBindingIDs,
			RequestedConfidentiality:         given.RequestedConfidentiality, RouteCeiling: given.RouteCeiling,
		})
	case "HUMAN_INTERACTION_MODEL":
		var given struct {
			OriginKind                   kernel.PrincipalKind       `json:"originKind"`
			OriginActorFQN               *string                    `json:"originActorFqn"`
			OriginExecutionID            *kernel.UUIDv7             `json:"originExecutionId"`
			SelectedHumanIDs             []string                   `json:"selectedHumanIds"`
			AcceptedHumanIDs             []string                   `json:"acceptedHumanIds"`
			QuestionRevision             uint64                     `json:"questionRevision"`
			ParticipantActive            bool                       `json:"participantActive"`
			AuthenticationValid          bool                       `json:"authenticationValid"`
			ResponseSpecificationMatches bool                       `json:"responseSpecificationMatches"`
			Duplicate                    bool                       `json:"duplicate"`
			RenderedDiffers              bool                       `json:"renderedDiffers"`
			MaterialEquivalenceProven    bool                       `json:"materialEquivalenceProven"`
			CredentialVerified           bool                       `json:"credentialVerified"`
			CommitSucceeded              bool                       `json:"commitSucceeded"`
			ResponsePolicy               kernel.HumanResponsePolicy `json:"responsePolicy"`
			DeclaredEffect               string                     `json:"declaredEffect"`
			ScopedCommandAuthority       bool                       `json:"scopedCommandAuthority"`
			DeadlineExpired              bool                       `json:"deadlineExpired"`
		}
		var when struct {
			Action           kernel.HumanInteractionPolicyAction `json:"action"`
			SourceKind       kernel.PrincipalKind                `json:"sourceKind"`
			ActorFQN         *string                             `json:"actorFqn"`
			RespondentID     string                              `json:"respondentId"`
			QuestionRevision uint64                              `json:"questionRevision"`
			SilenceIsConsent bool                                `json:"silenceIsConsent"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.EvaluateHumanInteractionPolicy(kernel.HumanInteractionPolicyInput{
			Action: when.Action, OriginKind: given.OriginKind, OriginActorPresent: given.OriginActorFQN != nil,
			OriginExecutionPresent: given.OriginExecutionID != nil, SourceKind: when.SourceKind, ActorFQNPresent: when.ActorFQN != nil,
			SelectedHumanIDs: given.SelectedHumanIDs, AcceptedHumanIDs: given.AcceptedHumanIDs, RespondentID: when.RespondentID,
			QuestionRevision: given.QuestionRevision, PresentedQuestionRevision: when.QuestionRevision,
			ParticipantActive: given.ParticipantActive, AuthenticationValid: given.AuthenticationValid,
			ResponseSpecificationMatch: given.ResponseSpecificationMatches, Duplicate: given.Duplicate,
			RenderedDiffers: given.RenderedDiffers, MaterialEquivalenceProven: given.MaterialEquivalenceProven,
			CredentialVerified: given.CredentialVerified, CommitSucceeded: given.CommitSucceeded,
			ResponsePolicy: given.ResponsePolicy, DeclaredEffect: given.DeclaredEffect,
			ScopedCommandAuthority: given.ScopedCommandAuthority, DeadlineExpired: given.DeadlineExpired,
			SilenceIsConsent: when.SilenceIsConsent,
		})
	case "TEAM_CONTINUITY_MODEL":
		var given struct {
			State                  kernel.ContinuityControlState `json:"state"`
			PowerEpoch             uint64                        `json:"powerEpoch"`
			AdmissionOpen          bool                          `json:"admissionOpen"`
			OperatingPosture       string                        `json:"operatingPosture"`
			UnresolvedExecutionIDs []kernel.UUIDv7               `json:"unresolvedExecutionIds"`
		}
		var when struct {
			Action                    kernel.TeamContinuityPolicyAction `json:"action"`
			PowerEpoch                uint64                            `json:"powerEpoch"`
			NextPowerEpoch            uint64                            `json:"nextPowerEpoch"`
			AllExecutionsRecorded     bool                              `json:"allExecutionsRecorded"`
			UnresolvedExecutionIDs    []kernel.UUIDv7                   `json:"unresolvedExecutionIds"`
			ReconciledExecutionIDs    []kernel.UUIDv7                   `json:"reconciledExecutionIds"`
			KnownInFlightExecutionIDs []kernel.UUIDv7                   `json:"knownInFlightExecutionIds"`
			ServicesHealthy           bool                              `json:"servicesHealthy"`
			OutboxReconciled          bool                              `json:"outboxReconciled"`
			ChangeStreamReconciled    bool                              `json:"changeStreamReconciled"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.EvaluateTeamContinuityPolicy(kernel.TeamContinuityPolicyInput{
			Action: when.Action, State: given.State, PowerEpoch: given.PowerEpoch, AdmissionOpen: given.AdmissionOpen,
			OperatingPosture: given.OperatingPosture, UnresolvedExecutionIDs: given.UnresolvedExecutionIDs,
			PresentedPowerEpoch: when.PowerEpoch, NextPowerEpoch: when.NextPowerEpoch,
			AllExecutionsRecorded: when.AllExecutionsRecorded, RecordedUnresolvedIDs: when.UnresolvedExecutionIDs,
			ReconciledExecutionIDs: when.ReconciledExecutionIDs,
			KnownInFlightIDs:       when.KnownInFlightExecutionIDs, ServicesHealthy: when.ServicesHealthy,
			OutboxReconciled: when.OutboxReconciled, ChangeStreamReconciled: when.ChangeStreamReconciled,
		})
	case "INVOCATION_ADMISSION_MODEL":
		var given kernel.InvocationAdmissionScenarioGiven
		var when kernel.InvocationAdmissionScenarioAction
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.EvaluateInvocationAdmissionScenario(given, when)
	case "WORK_BUDGET_MODEL":
		var given kernel.WorkBudgetScenarioGiven
		var when kernel.WorkBudgetScenarioAction
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		return kernel.EvaluateWorkBudgetScenario(given, when)
	case "PROJECTION_MODEL":
		var given struct {
			Revision    uint64 `json:"revision"`
			LastEventID string `json:"lastEventId"`
			LastDigest  string `json:"lastDigest"`
			Incremental any    `json:"incremental"`
		}
		var when struct {
			Action string `json:"action"`
			Event  struct {
				Revision uint64 `json:"revision"`
				EventID  string `json:"eventId"`
				Digest   string `json:"digest"`
			} `json:"event"`
			Rebuilt any `json:"rebuilt"`
		}
		decode(t, item.Given, &given)
		decode(t, item.When, &when)
		if when.Action == "COMPARE_REBUILD" {
			if reflect.DeepEqual(given.Incremental, when.Rebuilt) {
				return struct {
					Accepted bool   `json:"accepted"`
					Reason   string `json:"reason"`
				}{true, "EXACT_REBUILD_MATCH"}
			}
			return struct {
				Accepted bool   `json:"accepted"`
				Reason   string `json:"reason"`
			}{false, "REBUILD_MISMATCH"}
		}
		result := struct {
			Accepted bool   `json:"accepted"`
			Reason   string `json:"reason"`
			Revision uint64 `json:"revision"`
		}{Revision: given.Revision}
		switch {
		case when.Event.Revision == given.Revision && when.Event.EventID == given.LastEventID && when.Event.Digest == given.LastDigest:
			result.Accepted, result.Reason = true, "DUPLICATE"
		case when.Event.Revision > given.Revision+1:
			result.Reason = "REVISION_GAP"
		case when.Event.Revision <= given.Revision:
			result.Reason = "REVISION_CONFLICT"
		default:
			result.Accepted, result.Reason, result.Revision = true, "APPLIED", when.Event.Revision
		}
		return result
	case "AUTHORITY_BOUNDARY_MODEL":
		var when struct {
			Source string `json:"source"`
			Effect string `json:"effect"`
		}
		decode(t, item.When, &when)
		result := struct {
			Accepted bool   `json:"accepted"`
			Reason   string `json:"reason"`
		}{}
		if when.Source == "SMA" && when.Effect == "SEMANTIC_MEMORY" {
			result.Accepted, result.Reason = true, "SMA_MEMORY_DOMAIN"
		} else if when.Source == "TEAMS_KERNEL" {
			result.Accepted, result.Reason = true, "ACCEPTED"
		} else {
			result.Reason = "TEAMS_AUTHORITY_REQUIRED"
		}
		return result
	default:
		t.Fatalf("unsupported fixture kind %q", item.Kind)
		return nil
	}
}

func loadFixtures(t *testing.T, path string) []fixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixtures %s: %v", path, err)
	}
	var document fixtureDocument
	decode(t, data, &document)
	return document.Fixtures
}

func locateRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate conformance test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func decode(t *testing.T, data []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func assertJSONEqual(t *testing.T, expected json.RawMessage, actual any) {
	t.Helper()
	actualBytes, err := json.Marshal(actual)
	if err != nil {
		t.Fatalf("marshal actual result: %v", err)
	}
	var expectedValue any
	var actualValue any
	decode(t, expected, &expectedValue)
	decode(t, actualBytes, &actualValue)
	if !reflect.DeepEqual(expectedValue, actualValue) {
		t.Fatalf("result mismatch\nexpected: %s\nactual:   %s", expected, actualBytes)
	}
}
