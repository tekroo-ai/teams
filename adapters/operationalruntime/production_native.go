package operationalruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
		features, err := service.Store.ListFeatures(ctx, nativePlanningInspectionStatuses(), 1000)
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
		}
		if result.ResultInput == nil {
			return nativeagent.Config{}, nativeagent.ErrInvalidBinding
		}
	}
	if consultationSource != nil {
		ref, err := nativeagent.BindContextFork(ctx, service.Store, brief, *consultationSource)
		if err != nil {
			return nativeagent.Config{}, err
		}
		result.ContextSource, result.ContextInvocation = ref, consultationSource
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
	feature, stage, _, round, planning, err := service.planningFeatureForTask(ctx, brief.Task.TaskID)
	if err != nil {
		return nativeagent.Config{}, err
	}
	if planning {
		result.ResultArrayLimits = featureStageResultArrayLimits(stage, feature.Input.MaximumTasks)
	}
	if planning && stage == stageArchitecture && round == 1 && feature.DesignRevision != nil {
		// This is a new forward node, not a restart of the completed design.
		// Resolve the original invocation from Teams state, never a model ID.
		source, _, sourceErr := service.featureArchitectureCandidateForRound(ctx, feature, 0)
		if sourceErr != nil || source.OutputDigest == nil || *source.OutputDigest != feature.DesignRevision.PriorDesignDigest {
			return nativeagent.Config{}, errors.Join(nativeagent.ErrInvalidBinding, sourceErr)
		}
		ref, bindErr := nativeagent.BindContextFork(ctx, service.Store, brief, source)
		if bindErr != nil {
			return nativeagent.Config{}, bindErr
		}
		result.ContextSource, result.ContextInvocation = ref, &source
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
			return service.validateFeatureStageOutputWithEvidence(finalCtx, feature, stage, output)
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
