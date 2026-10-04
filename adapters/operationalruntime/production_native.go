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
			effectTools = append(effectTools, definition.Name)
		}
	}
	slices.Sort(effectTools)
	var resultContract *nativeagent.ResultContract
	if brief.MessageHandler == nil && brief.ResultProtocol != nil && (brief.Purpose == kernel.PurposeHandoff || brief.Purpose == kernel.PurposeReplan) {
		for _, definition := range workflows {
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
			BaseURL: settings.BaseURL, Model: settings.Model, MaxOutputTokens: settings.MaxOutputTokens, MaxTurns: settings.MaxTurns,
			AllowedReadTools: readTools, AllowedEffectTools: effectTools,
		},
		ResultContract: resultContract,
	}
	if len(effectTools) > 0 {
		result.Effects = service.Store
	}
	if strings.HasPrefix(brief.Scope.WorktreeID, "candidate-") {
		workspace, err := service.workspaceResolver.ResolveWorkspace(ctx, brief.Scope)
		if err != nil || workspace.Candidate == nil {
			return nativeagent.Config{}, nativeagent.ErrInvalidBinding
		}
		result.Candidate = &nativeagent.CandidateBinding{ID: kernel.UUIDv7(workspace.Candidate.CandidateID), ReceiptSHA256: workspace.Candidate.ReceiptSHA256}
	}
	feature, stage, _, _, planning, err := service.planningFeatureForTask(ctx, brief.Task.TaskID)
	if err != nil {
		return nativeagent.Config{}, err
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
