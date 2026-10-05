package operationalruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestFileBackedNativeSelectionBindsProfileAndTools(t *testing.T) {
	path, config := writeNativeProductionFixture(t)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ExecutionBackend != "native" || loaded.OpenHands.BaseURL != "" || loaded.Profiles[0].NativeSettings == nil {
		t.Fatalf("native configuration was not selected: backend=%q OpenHands=%q", loaded.ExecutionBackend, loaded.OpenHands.BaseURL)
	}
	config.Profiles[0].ToolPolicyDigest = repeatedDigest('4') // old OpenHands surface
	writeJSON(t, path, config, 0o600)
	if _, err := LoadProductionConfig(path); err == nil {
		t.Fatal("OpenHands tool surface was accepted for native execution")
	}
}

func TestNativeGoExecutableBindingSurvivesMinimalPATH(t *testing.T) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goPath, err = filepath.EvalSymlinks(goPath)
	if err != nil {
		t.Fatal(err)
	}
	path, config := writeNativeProductionFixture(t)
	config.Native.GoBinary = goPath
	writeJSON(t, path, config, 0o600)
	// Model the launchd environment: no login-shell/Homebrew PATH entry.
	t.Setenv("PATH", t.TempDir())
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveProductionConfig(loaded)
	if err != nil || resolved.nativeGoBinary != goPath {
		t.Fatalf("explicit toolchain was lost: %q, %v", resolved.nativeGoBinary, err)
	}
	if _, err := resolveNativeGoBinary(""); err == nil {
		t.Fatal("missing toolchain was not rejected before agent work")
	}
	if _, err := resolveNativeGoBinary("go"); err == nil {
		t.Fatal("relative configured executable accepted")
	}
	if _, err := resolveNativeGoBinary(t.TempDir()); err == nil {
		t.Fatal("directory accepted as an executable")
	}
	missing := filepath.Join(t.TempDir(), "go")
	if _, err := resolveNativeGoBinary(missing); err == nil {
		t.Fatal("missing configured toolchain accepted")
	}
	if err := os.WriteFile(missing, []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveNativeGoBinary(missing); err == nil {
		t.Fatal("non-executable file accepted")
	}
}

func TestFileBackedNativeSelectionRejectsTamperedSchemaAndOpenHandsSettings(t *testing.T) {
	path, config := writeNativeProductionFixture(t)
	config.Native.ResultSchemas[0].SHA256 = repeatedDigest('e')
	writeJSON(t, path, config, 0o600)
	if _, err := LoadProductionConfig(path); err == nil {
		t.Fatal("tampered native result schema was accepted")
	}
	path, config = writeNativeProductionFixture(t)
	config.Profiles[0].NativeSettings = nil
	config.Profiles[0].AgentSettings = json.RawMessage(`{"kind":"Agent"}`)
	writeJSON(t, path, config, 0o600)
	if _, err := LoadProductionConfig(path); err == nil {
		t.Fatal("OpenHands settings were accepted as a native model profile")
	}
}

func writeNativeProductionFixture(t *testing.T) (string, ProductionConfig) {
	t.Helper()
	path, config := writeProductionFixture(t)
	directory := filepath.Dir(path)
	settings := nativeagent.ProfileSettings{SchemaVersion: nativeagent.ProfileSettingsVersion, BaseURL: "http://127.0.0.1:8800/v1", Model: "local-fixture", MaxOutputTokens: 8192}
	profile := &config.Profiles[0]
	digest, err := nativeagent.ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, settings)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := agenttools.NativeToolSurfaceDigest([]string{"repository.edit"})
	if err != nil {
		t.Fatal(err)
	}
	profile.ModelProfileDigest = digest
	profile.ToolPolicyDigest = surface
	profile.AgentSettings = nil
	profile.NativeSettings = &settings
	profile.MaximumIterations = 0
	profile.QualificationCorpus, profile.Qualification = testQualificationBundle(t, digest, "coder", profile.DecisionRoute, surface, allTestWorkKinds(), time.Now().UTC().Add(-time.Minute))
	manifestPath := filepath.Join(directory, "team.json")
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Roles[0].ModelProfileDigest = digest
	writeJSON(t, manifestPath, manifest, 0o600)
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(manifestRaw)
	config.Organization.ManifestDigest = kernel.Digest(hex.EncodeToString(manifestSum[:]))
	definition := kernel.WorkflowDefinition{
		SchemaVersion: kernel.WorkflowDefinitionSchemaVersion, Name: "publication", Version: "1.0.0", TriggerTypes: []string{"publication.requested"},
		Stages:      []kernel.WorkflowStageDefinition{{StageID: "publish", DependsOn: []string{}, InputSchema: "request/v1", OutputSchema: "publication/v1", RequiredCapabilities: []string{"implement"}, PreferredFQRNs: []kernel.RoleFQRN{"coder"}, Purpose: kernel.WorkflowPurposeImplementation, Risk: kernel.WorkflowRiskLow, ConcurrencyGroup: "publication", MaximumParallelism: 1, AttemptLimit: 1, AllowedOutgoingPurposes: []string{}, TargetSelection: kernel.WorkflowTargetCapability, ValidationPolicy: kernel.WorkflowValidationDeterministic}},
		RootBudgets: kernel.WorkflowBudgetLimits{MaximumModelInvocations: 1, MaximumHops: 2, MaximumAttempts: 1}, ProjectionRules: []string{},
	}
	definition.ContentDigest, err = definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "publication.workflow.json"), definition, 0o600)
	config.Organization.WorkflowDefinitions = []ProductionWorkflowSource{{DefinitionFile: "publication.workflow.json", DefinitionDigest: definition.ContentDigest}}
	schema := []byte("{\"type\":\"object\",\"additionalProperties\":false}")
	if err := os.WriteFile(filepath.Join(directory, "publication.schema.json"), schema, 0o600); err != nil {
		t.Fatal(err)
	}
	schemaSum := sha256.Sum256(schema)
	config.ExecutionBackend = "native"
	config.Native = &ProductionNativeConfig{Owner: "fixture-native", LeaseDuration: "90s", Heartbeat: "15s", RequestTimeout: "20m", WorkflowTriggers: []string{"publication.requested"}, ResultSchemas: []ProductionNativeResultSchema{{Reference: "publication/v1", Path: "publication.schema.json", SHA256: kernel.Digest(hex.EncodeToString(schemaSum[:]))}}}
	config.OpenHands = ProductionOpenHandsConfig{}
	writeJSON(t, path, config, 0o600)
	return path, config
}
