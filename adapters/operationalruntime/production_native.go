package operationalruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func loadProductionNativeResultSchemas(sources []ProductionNativeResultSchema) (map[string]NativeResultSchema, error) {
	if len(sources) == 0 || len(sources) > 128 {
		return nil, invalidConfig("native result schema set is empty or too large")
	}
	result := make(map[string]NativeResultSchema, len(sources))
	for _, source := range sources {
		if source.Reference == "" || len(source.Reference) > 256 || !filepath.IsAbs(source.Path) || !source.SHA256.Valid() {
			return nil, invalidConfig("native result schema binding is invalid")
		}
		if _, duplicate := result[source.Reference]; duplicate {
			return nil, invalidConfig("native result schema reference is duplicated")
		}
		info, err := os.Stat(source.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 1<<20 {
			return nil, invalidConfig("native result schema file is missing or oversized")
		}
		encoded, err := os.ReadFile(source.Path)
		if err != nil || !json.Valid(encoded) || !bytes.HasPrefix(bytes.TrimSpace(encoded), []byte("{")) {
			return nil, invalidConfig("native result schema is not a JSON object")
		}
		sum := sha256.Sum256(encoded)
		if source.SHA256 != kernel.Digest(hex.EncodeToString(sum[:])) {
			return nil, invalidConfig("native result schema digest does not match")
		}
		result[source.Reference] = NativeResultSchema{SHA256: source.SHA256, JSON: append(json.RawMessage(nil), encoded...)}
	}
	return result, nil
}

func configuredNativeSession(ctx context.Context, service *ProductionService, profiles map[kernel.Digest]ProductionProfile, workflows []kernel.WorkflowDefinition, schemas map[string]NativeResultSchema, requestTimeout time.Duration, brief application.ExecutionBrief) (nativeagent.Config, error) {
	if service == nil || service.Runtime == nil || service.Store == nil {
		return nativeagent.Config{}, nativeagent.ErrInvalidBinding
	}
	profile, found := profiles[brief.ModelProfileDigest]
	if !found || profile.NativeSettings == nil {
		return nativeagent.Config{}, nativeagent.ErrInvalidBinding
	}
	permissions := brief.RoleGrounding.Permissions
	readTools := make([]string, 0)
	for _, definition := range agenttools.ReadOnlyDefinitions(permissions) {
		readTools = append(readTools, definition.Name)
	}
	slices.Sort(readTools)
	effectTools := make([]string, 0)
	if brief.Purpose == kernel.PurposeImplementation || brief.Purpose == kernel.PurposeRepair {
		for _, definition := range agenttools.MutationDefinitions(permissions) {
			effectTools = append(effectTools, definition.Name)
		}
	}
	if brief.Purpose == kernel.PurposeValidation || brief.Purpose == kernel.PurposeImplementation || brief.Purpose == kernel.PurposeRepair {
		for _, definition := range agenttools.TestDefinitions(permissions) {
			if brief.Purpose == kernel.PurposeValidation && (definition.Name == "run_go_tests_worktree" || definition.Name == "check_go_build_worktree") {
				continue
			}
			effectTools = append(effectTools, definition.Name)
		}
	}
	slices.Sort(effectTools)
	var resultContract *nativeagent.ResultContract
	var consultationSource *kernel.WorkInvocation
	amendmentAssessment := false
	if brief.MessageHandler == nil && brief.Purpose == kernel.PurposeHandoff {
		var err error
		resultContract, consultationSource, err = service.nativeTaskConsultation(ctx, brief)
		if err != nil {
			return nativeagent.Config{}, err
		}
		if resultContract == nil {
			resultContract, consultationSource, err = service.nativePlanAmendment(ctx, brief)
			if err != nil {
				return nativeagent.Config{}, err
			}
			amendmentAssessment = resultContract != nil
		}
	}
	if brief.MessageHandler == nil && brief.ResultProtocol != nil && (brief.Purpose == kernel.PurposeHandoff || brief.Purpose == kernel.PurposeReplan) {
		for _, definition := range workflows {
			if consultationSource != nil {
				break
			}
			candidate, err := ResolveNativeWorkflowResultContract(brief, definition, schemas)
			if err != nil {
				continue
			}
			if resultContract != nil {
				return nativeagent.Config{}, nativeagent.ErrInvalidBinding
			}
			resultContract = candidate
		}
		if resultContract == nil {
			return nativeagent.Config{}, nativeagent.ErrInvalidBinding
		}
	}
	settings := profile.NativeSettings
	result := nativeagent.Config{
		Bindings:       service.Runtime.toolGateway.Bindings,
		Gateway:        service.Runtime.toolGateway,
		EvidenceReader: newNativeEvidenceResolver(service.Store, service.Runtime.evidence, service.evidenceRoot, brief.Evidence),
		HTTP:           &http.Client{Timeout: requestTimeout},
		Profile: nativeagent.Profile{
			RoleFQRN: profile.RoleFQRN, RoleBundleDigest: profile.RoleBundleDigest,
			ModelProfileDigest: profile.ModelProfileDigest, RuntimeIdentityDigest: profile.RuntimeIdentityDigest,
			ToolPolicyDigest: profile.ToolPolicyDigest, EffectPolicyDigest: profile.EffectPolicyDigest,
			BaseURL: settings.BaseURL, Model: settings.Model, ModelInstructions: settings.ModelInstructions, ResponseMode: settings.ResponseMode, MaxOutputTokens: settings.MaxOutputTokens, MaxTurns: settings.MaxTurns,
			Reasoning:        settings.Reasoning,
			AllowedReadTools: readTools, AllowedEffectTools: effectTools,
		},
		ResultContract: resultContract,
	}
	if len(effectTools) > 0 {
		result.Effects = service.Store
	}
	if amendmentAssessment {
		features, err := service.nativeBriefFeatures(ctx, brief)
		if err != nil {
			return nativeagent.Config{}, err
		}
		for _, feature := range features {
			if feature.PlanAmendment == nil || feature.PlanAmendment.AssessmentID != brief.Task.TaskID {
				continue
			}
			input := feature.PlanAmendment.AssessmentInput
			if input == nil || !slices.Contains(brief.Evidence, *input) {
				return nativeagent.Config{}, nativeagent.ErrInvalidBinding
			}
			raw, err := service.Runtime.ReadExecutionOutput(ctx, input.SHA256)
			if err != nil || digestBytes(raw) != input.SHA256 {
				return nativeagent.Config{}, errors.Join(nativeagent.ErrInvalidBinding, err)
			}
			result.ResultInput = &nativeagent.ResultInput{Evidence: *input, Value: append(json.RawMessage(nil), raw...)}
			if feature.PlanAmendment.State == organization.AmendmentAssessing {
				featureID := feature.ID
				result.ValidateFinalResult = func(finalCtx context.Context, output []byte) error {
					current, found, err := service.Store.LoadFeature(finalCtx, featureID)
					if err != nil || !found || current.PlanAmendment == nil || current.PlanAmendment.AssessmentID != brief.Task.TaskID {
						return errors.Join(nativeagent.ErrInvalidBinding, err)
					}
					witness, _, _, err := service.amendmentWitness(finalCtx, current, current.PlanAmendment.BlockedInvocationID, current.PlanAmendment.ConsultationID)
					if err != nil {
						return err
					}
					witness.PM, witness.PMExecution = brief.ActorFQN, brief.Execution
					raw := bytes.TrimSpace(bytes.TrimPrefix(output, []byte(application.OrganizationalResultMarker)))
					decision := organization.CheckPlanAmendmentDraft(current, raw, witness, service.planning.PlanAmendment.Policy)
					if !decision.Accepted {
						return fmt.Errorf("%w: amendment draft rejected: %s; correct the proposal using the authoritative reservation, remaining_task_allowances and cumulative task budget; no decision or publication has occurred", organization.ErrPlanAmendment, decision.Code)
					}
					return nil
				}
			}
		}
		if result.ResultInput == nil {
			return nativeagent.Config{}, nativeagent.ErrInvalidBinding
		}
	}
	if consultationSource != nil && result.ValidateFinalResult == nil {
		result.ValidateFinalResult = func(_ context.Context, output []byte) error {
			if amendmentAssessment {
				raw := bytes.TrimSpace(bytes.TrimPrefix(output, []byte(application.OrganizationalResultMarker)))
				return organization.ValidatePlanAmendmentProposalJSON(raw)
			}
			_, _, err := consultationAnswer(output)
			return err
		}
	}
	if strings.HasPrefix(brief.Scope.WorktreeID, "candidate-") {
		workspace, err := service.workspaceResolver.ResolveWorkspace(ctx, brief.Scope)
		if err != nil || workspace.Candidate == nil {
			return nativeagent.Config{}, nativeagent.ErrInvalidBinding
		}
		result.Candidate = &nativeagent.CandidateBinding{ID: kernel.UUIDv7(workspace.Candidate.CandidateID), ReceiptSHA256: workspace.Candidate.ReceiptSHA256}
	}
	var feature organization.FeatureRequest
	var stage featurePlanningStage
	var round uint32
	var planning bool
	if consultationSource == nil {
		var err error
		feature, stage, _, round, planning, err = service.planningFeatureForTask(ctx, brief.Task.TaskID)
		if err != nil {
			return nativeagent.Config{}, err
		}
	}
	if planning && brief.Task.StoryID != deterministicOperationalUUID("feature-planning-story", string(feature.ID)) || !planning && brief.Task.StoryID == deterministicOperationalUUID("feature-planning-story", string(feature.ID)) {
		return nativeagent.Config{}, nativeagent.ErrInvalidBinding
	}
	if planning {
		result.ResultArrayLimits = featureStageResultArrayLimits(stage, service.planning.initialTaskLimit(feature))
		if stage == stageArchitecture {
			routing, routingErr := service.workflowTaskRoutingPolicy(service.clock.Now().UTC())
			if routingErr != nil {
				return nativeagent.Config{}, routingErr
			}
			allowed := make([]string, 0, len(routing.PurposeRoutes))
			for _, route := range routing.PurposeRoutes {
				allowed = append(allowed, string(route.Purpose))
			}
			result.ResultEnumLimits = []nativeagent.ResultEnumLimit{{PropertyPath: []string{"work_product", "tasks", "*", "purpose"}, Values: allowed}}
		}
	}
	returnSource, recordedContext, sourceErr := service.recordedReturnContext(ctx, brief)
	if sourceErr != nil {
		return nativeagent.Config{}, sourceErr
	}
	var returnBinding *nativeagent.ContextReturnBinding
	if brief.RetryOfInvocationID != nil && (!recordedContext || returnSource != nil) {
		resolved, binding, retryErr := service.nativeRetryContext(ctx, brief)
		if retryErr != nil || recordedContext && resolved != nil && resolved.ID != returnSource.ID {
			return nativeagent.Config{}, errors.Join(nativeagent.ErrInvalidBinding, retryErr)
		}
		returnBinding = binding
		if !recordedContext {
			returnSource = resolved
		}
	} else if !recordedContext {
		returnSource, sourceErr = service.nativeReturnedInvocation(ctx, brief)
		if sourceErr != nil {
			return nativeagent.Config{}, sourceErr
		}
	}
	if !recordedContext && returnSource == nil && planning {
		returnSource, sourceErr = service.planningReturnSource(ctx, feature, stage, round)
		if sourceErr != nil {
			return nativeagent.Config{}, sourceErr
		}
	}
	if returnSource == nil && consultationSource != nil {
		returnSource = consultationSource
	}
	if returnSource != nil {
		if !decisionOwnerReturnCompatible(brief, consultationSource, returnSource) {
			return nativeagent.Config{}, nativeagent.ErrInvalidBinding
		}
		if returnBinding != nil {
			returnBinding.SourceInvocationID, returnBinding.SourceTaskID = returnSource.ID, returnSource.TaskID
		}
		if !slices.Contains(brief.Task.DependsOn, returnSource.TaskID) && returnSource.TaskID != brief.Task.TaskID && brief.Purpose != kernel.PurposeRepair {
			correction, err := service.correctedPlanningReturn(ctx, feature, stage, round, planning, brief, *returnSource)
			sourceErr = err
			if sourceErr != nil {
				return nativeagent.Config{}, sourceErr
			}
			if returnBinding == nil {
				returnBinding = correction
			} else {
				returnBinding.CorrectionEvidence = correction.CorrectionEvidence
			}
		}
		ref, bindErr := nativeagent.BindContextFork(ctx, service.Store, brief, *returnSource, returnBinding)
		if bindErr != nil {
			return nativeagent.Config{}, bindErr
		}
		result.ContextSource, result.ContextInvocation = ref, returnSource
		result.ContextReturn = returnBinding
	}
	if planning && stage == stagePlanFinalization {
		_, architectureOutput, sourceErr := service.featureArchitectureCandidate(ctx, feature)
		if sourceErr != nil || feature.Design == nil || digestBytes(architectureOutput) != feature.Design.OutputDigest {
			return nativeagent.Config{}, errors.Join(nativeagent.ErrInvalidBinding, sourceErr)
		}
		result.PlanFinalization = &nativeagent.PlanFinalizationBinding{SourceOutput: append([]byte(nil), architectureOutput...), SourceDigest: feature.Design.OutputDigest}
	}
	if planning {
		result.ValidateFinalResult = func(finalCtx context.Context, output []byte) error {
			if err := service.validateFeatureStageOutputWithEvidence(finalCtx, feature, stage, output); err != nil {
				return err
			}
			if _, _, reported := planningNonCompletion(stage, output); reported {
				return nil
			}
			if stage != stageArchitecture && stage != stagePlanFinalization {
				return nil
			}
			workspace, err := service.workspaceResolver.ResolveWorkspace(finalCtx, brief.Scope)
			if err != nil {
				return err
			}
			var plan architectureStageResult
			if stage == stageArchitecture {
				plan, err = parseArchitectureStageResult(output, featureInputOperationalMarkers(feature), featureAuthorizedActorFQNs(feature)...)
			} else {
				plan, err = parsePlanFinalizationStageResult(output, featureInputOperationalMarkers(feature), featureAuthorizedActorFQNs(feature)...)
			}
			if err != nil {
				return err
			}
			return checkPlanGoDeclarations(finalCtx, workspace.WorkingDirectory, plan)
		}
	}
	return result, nil
}

func featureStageResultArrayLimits(stage featurePlanningStage, maximumTasks uint32) []nativeagent.ResultArrayLimit {
	if stage != stageArchitecture {
		return nil
	}
	return []nativeagent.ResultArrayLimit{{
		PropertyPath: []string{"work_product", "tasks"},
		MaxItems:     int(maximumBudgetedImplementationTasks(maximumTasks)),
	}}
}
