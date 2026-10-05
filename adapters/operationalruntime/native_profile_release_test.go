//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type measuredNativeInvocation struct {
	Invocation kernel.WorkInvocation `json:"invocation"`
	Journal    []agentruntime.Entry  `json:"journal"`
	Output     []byte                `json:"output"`
}

// This release preparation consumes retained real executions, not the fixture's
// synthetic admission records. Missing roles run directly against authenticated
// role packages in a disposable repository, without a kernel admission bypass.
// It writes a new candidate config only; it never replaces the active config.
func TestPrepareMeasuredNativeProfileRelease(t *testing.T) {
	source, evidencePath, output := os.Getenv("TEKROO_NATIVE_RELEASE_CONFIG"), os.Getenv("TEKROO_NATIVE_RELEASE_EVIDENCE"), os.Getenv("TEKROO_NATIVE_RELEASE_OUTPUT")
	if source == "" || evidencePath == "" || output == "" {
		t.Skip("explicit release paths required")
	}
	config, err := LoadProductionConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	if digestBytes(raw) != "849025e4aed7d0221fd89ae6f7055f0e22656be88ae9ff24c08de219037c6464" {
		t.Fatal("measured source receipt changed")
	}
	var measured struct {
		MeasuredAt time.Time           `json:"measured_at"`
		Profiles   []ProductionProfile `json:"profiles"`
		Tasks      []struct {
			Invocations []measuredNativeInvocation `json:"invocations"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(output, name), append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("source-measurement.json", json.RawMessage(raw))
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(config.Organization.ManifestFile, &manifest); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(config.Organization.ManifestFile); err != nil || digestBytes(content) != config.Organization.ManifestDigest {
		t.Fatal("source manifest changed")
	}
	keys := map[string]ed25519.PublicKey{}
	for _, publisher := range config.Organization.Publishers {
		value, err := os.ReadFile(publisher.PublicKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(value)))
		if err != nil || len(key) != ed25519.PublicKeySize {
			t.Fatal("invalid publisher key")
		}
		keys[publisher.KeyID] = key
	}
	coderPath, err := filepath.Abs("../../config/starter-team/transport-neutral-20260929/roles-v4/coder-2.1.1/role.json")
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(filepath.Dir(coderPath), "publisher.pub")
	publicRaw, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatal("invalid coder key")
	}
	keys["tekroo-native-coder-20261003"] = publicKey
	config.Organization.Publishers = append(config.Organization.Publishers, ProductionPublisher{KeyID: "tekroo-native-coder-20261003", PublicKeyFile: publicPath})
	roles := make(map[string]organization.RoleBundle)
	packages := make(map[string]organization.LoadedRolePackage)
	if err := os.Mkdir(filepath.Join(output, "roles"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Roles {
		binding := &manifest.Roles[i]
		path := filepath.Join(filepath.Dir(config.Organization.ManifestFile), binding.BundlePath)
		if binding.Role == "coder" {
			path = coderPath
			binding.BundleDigest = "36a61a8670581b83dd7a3ea448350b54c7e3eade701d67849e0cdd0e7ad3e64e"
			binding.PublisherKeyID = "tekroo-native-coder-20261003"
		}
		bundle, err := organization.LoadRoleBundle(path, binding.BundleDigest, binding.PublisherKeyID, keys)
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := organization.LoadRolePackage(path, bundle)
		if err != nil {
			t.Fatal(err)
		}
		roles[binding.Role], packages[binding.Role] = bundle, pkg
		if err := os.Symlink(filepath.Dir(path), filepath.Join(output, "roles", binding.Role)); err != nil {
			t.Fatal(err)
		}
		binding.BundlePath = filepath.Join("roles", binding.Role, "role.json")
	}
	observations := map[string][]measuredNativeInvocation{}
	for _, task := range measured.Tasks {
		for _, invocation := range task.Invocations {
			role, err := kernel.RoleFQRNFromActor(invocation.Invocation.ActorFQN)
			if err != nil {
				t.Fatal(err)
			}
			assertNativeMeasurement(t, invocation)
			observations[string(role)] = append(observations[string(role)], invocation)
		}
	}
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	config.Profiles = measured.Profiles
	root := t.TempDir()
	workspaces, _, _ := nativeQualificationWorkspaces(t, root, config.Workspaces[:1])
	workspace := workspaces[0].WorkingDirectory
	for _, role := range []string{"security", "senior-coder"} {
		profile := slices.Clone(config.Profiles)
		selected := slices.IndexFunc(profile, func(p ProductionProfile) bool { return string(p.RoleFQRN) == role })
		if selected < 0 {
			t.Fatal("missing profile")
		}
		p := profile[selected]
		bundle, pkg := roles[role], packages[role]
		messageType := "tekroo.message.task.security-review-requested"
		purpose := kernel.PurposeReview
		description := "Review this small greeting repository for security issues. Report concrete findings, or explain why no material issue was found. Do not modify source."
		if role == "senior-coder" {
			messageType = "tekroo.message.task.escalated"
			purpose = kernel.PurposeEscalation
			description = "Diagnose why Greeting ignores its name argument. Inspect the source and tests, and provide the concrete minimal repair for the assigned coder. Do not modify source in this diagnostic qualification."
		}
		handler := pkg.Handlers[messageType]
		invocationID, err := ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		actor := kernel.ActorFQN("teams::" + role + "-1")
		brief := application.ExecutionBrief{ContractManifest: kernel.ContractIdentity, InvocationID: invocationID, ActorFQN: actor, Purpose: purpose, ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest,
			ResultProtocol:    &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Instruction: "Submit the result as a JSON object through the structured result channel."},
			RoleGrounding:     application.RoleExecutionGrounding{ActorFQN: actor, RoleFQRN: p.RoleFQRN, BundleVersion: bundle.Version, BundleDigest: p.RoleBundleDigest, Capabilities: bundle.Capabilities, Permissions: bundle.Permissions, Instructions: pkg.Charter},
			Task:              application.TaskExecutionSpecification{Title: "Native profile qualification", Description: description, AcceptanceCriteria: []string{"Ground the conclusion in the actual repository and return the handler result."}},
			MessageHandler:    &application.MessageHandlerGrounding{MessageType: messageType, Instructions: handler.Instructions, ResultSchema: handler.ResultSchema, ResultSchemaDigest: handler.Binding.ResultSchema.SHA256, AllowedResults: handler.Binding.AllowedResults},
			ExecutionGuidance: []string{"Read AGENTS.md before source. Return the result through the structured result channel."},
		}
		binding := nativeReleaseBinding{authority: agenttools.Authority{WorkspaceRoot: workspace, Permissions: bundle.Permissions, Purpose: purpose, EffectPolicyDigest: p.EffectPolicyDigest}}
		journal := &nativeReleaseJournal{path: filepath.Join(output, role+"-journal.json")}
		readTools := []string{}
		for _, tool := range agenttools.ReadOnlyDefinitions(bundle.Permissions) {
			readTools = append(readTools, tool.Name)
		}
		slices.Sort(readTools)
		settings := p.NativeSettings
		cfg := nativeagent.Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: 2 * time.Minute}}, Journal: journal, HTTP: &http.Client{Timeout: 20 * time.Minute}, Profile: nativeagent.Profile{RoleFQRN: p.RoleFQRN, RoleBundleDigest: p.RoleBundleDigest, ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest, BaseURL: settings.BaseURL, Model: settings.Model, ResponseMode: settings.ResponseMode, MaxOutputTokens: settings.MaxOutputTokens, AllowedReadTools: readTools}}
		encoded, _ := json.Marshal(brief)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		session, err := nativeagent.PrepareReadOnly(ctx, brief, digestBytes(encoded), cfg)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Logf("measuring %s invocation=%s", role, invocationID)
		result, err := session.Run(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := application.ValidateRoleHandlerResult(*brief.MessageHandler, []byte(result))
		if err != nil || parsed.Outcome != "completed" {
			t.Fatalf("%s result: %v outcome=%s", role, err, parsed.Outcome)
		}
		inv := measuredNativeInvocation{Invocation: kernel.WorkInvocation{ID: invocationID, ActorFQN: actor, State: kernel.InvocationSucceeded, ModelProfileDigest: p.ModelProfileDigest}, Journal: journal.entries, Output: []byte(result)}
		assertNativeMeasurement(t, inv)
		write(role+"-measurement.json", inv)
		observations[role] = []measuredNativeInvocation{inv}
	}
	threshold := digestBytes([]byte("Native real-model schema-action execution completes; zero tool errors, malformed finals, correction turns, or retries; result and journal retained."))
	for i := range config.Profiles {
		p := &config.Profiles[i]
		p.Qualification, p.QualificationCorpus = nil, nil
		bundle := roles[string(p.RoleFQRN)]
		expected, err := nativeagent.ModelProfileDigest(p.RoleFQRN, p.RoleBundleDigest, *p.NativeSettings)
		if err != nil || expected != p.ModelProfileDigest {
			t.Fatal("profile settings mismatch")
		}
		surface, err := agenttools.NativeToolSurfaceDigest(bundle.Permissions)
		if err != nil || surface != p.ToolPolicyDigest {
			t.Fatal("tool surface mismatch")
		}
		if p.RoleFQRN != "operator" {
			seen := observations[string(p.RoleFQRN)]
			if len(seen) == 0 {
				t.Fatal("unmeasured profile")
			}
			corpusID, idErr := ids.Next()
			if idErr != nil {
				t.Fatal(idErr)
			}
			qualID, idErr := ids.Next()
			if idErr != nil {
				t.Fatal(idErr)
			}
			kind := kernel.WorkDesign
			switch p.RoleFQRN {
			case "coder", "senior-coder":
				kind = kernel.WorkImplementation
			case "tester":
				kind = kernel.WorkValidation
			case "security":
				kind = kernel.WorkSecurityReview
			}
			kinds := []kernel.WorkKind{kind}
			if p.RoleFQRN == "product-owner" {
				kinds = append(kinds, kernel.WorkRelease)
			}
			corpus := application.QualificationCorpusDefinition{CorpusID: corpusID, Revision: 1, DecisionRoute: p.DecisionRoute, QualifiedRole: string(p.RoleFQRN), WorkKinds: kinds, ScenarioIDs: []string{"real-native-schema-actions-20261005"}, ThresholdDigest: threshold, ToolSurfaceDigest: p.ToolPolicyDigest}
			corpusDigest, err := corpus.Digest()
			if err != nil {
				t.Fatal(err)
			}
			qual := kernel.ModelProfileQualification{QualificationID: qualID, QualificationCorpusDigest: corpusDigest, ModelProfileDigest: p.ModelProfileDigest, DecisionRoute: p.DecisionRoute, QualifiedRole: string(p.RoleFQRN), QualifiedWorkKinds: kinds, Status: kernel.QualificationPass, ObservedAt: time.Now().UTC()}
			for _, observation := range seen {
				if observation.Invocation.ModelProfileDigest != p.ModelProfileDigest {
					t.Fatal("evidence/profile mismatch")
				}
				qual.EvidenceIDs = append(qual.EvidenceIDs, observation.Invocation.ID)
			}
			qual.QualificationDigest, err = application.QualificationDigest(qual)
			if err != nil {
				t.Fatal(err)
			}
			p.QualificationCorpus, p.Qualification = &corpus, &qual
			if !p.qualificationDefinitionValid() {
				t.Fatal("qualification bindings invalid")
			}
		}
		for j := range manifest.Roles {
			if manifest.Roles[j].Role == string(p.RoleFQRN) {
				manifest.Roles[j].ModelProfileDigest = p.ModelProfileDigest
			}
		}
	}
	write("team.json", manifest)
	config.Organization.ManifestFile = filepath.Join(output, "team.json")
	manifestRaw, _ := os.ReadFile(config.Organization.ManifestFile)
	config.Organization.ManifestDigest = digestBytes(manifestRaw)
	schemas := nativeQualificationSchemas(t, config.Organization.ManifestFile)
	refs := []string{}
	for ref := range schemas {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	bound := []ProductionNativeResultSchema{}
	for i, ref := range refs {
		name := strings.ReplaceAll(ref, "/", "-") + ".schema.json"
		write(name, schemas[ref].JSON)
		data, _ := os.ReadFile(filepath.Join(output, name))
		bound = append(bound, ProductionNativeResultSchema{Reference: refs[i], Path: filepath.Join(output, name), SHA256: digestBytes(data)})
	}
	config.ExecutionBackend = "native"
	config.OpenHands = ProductionOpenHandsConfig{}
	config.Native = &ProductionNativeConfig{Owner: "tekrood-production-native", LeaseDuration: "90s", Heartbeat: "15s", RequestTimeout: "20m", WorkflowTriggers: []string{"tekroo.message.feature.submitted"}, ResultSchemas: bound}
	write("tekrood.native-candidate.json", config)
	if _, err := LoadProductionConfig(filepath.Join(output, "tekrood.native-candidate.json")); err != nil {
		t.Fatal(err)
	}
	t.Logf("measured candidate ready: %s", filepath.Join(output, "tekrood.native-candidate.json"))
}

func assertNativeMeasurement(t *testing.T, observation measuredNativeInvocation) {
	t.Helper()
	if observation.Invocation.State != kernel.InvocationSucceeded || len(observation.Journal) < 2 || observation.Invocation.RetryOrdinal != 0 {
		t.Fatal("measurement did not succeed cleanly")
	}
	finals := 0
	for _, entry := range observation.Journal {
		if entry.Kind == agentruntime.ToolDone {
			var result agentruntime.ToolResult
			if json.Unmarshal(entry.Payload, &result) != nil || result.Error != "" {
				t.Fatalf("tool error in %s: %s", observation.Invocation.ID, entry.Payload)
			}
		}
		if entry.Kind == agentruntime.ModelTurn {
			var turn agentruntime.Completion
			if json.Unmarshal(entry.Payload, &turn) != nil {
				t.Fatal("malformed turn")
			}
			for _, call := range turn.ToolCalls {
				if call.Name == "submit_result" {
					finals++
					var args map[string]json.RawMessage
					if json.Unmarshal(call.Arguments, &args) != nil || len(args["work_product"]) == 0 || args["work_product"][0] != '{' {
						t.Fatal("non-object result")
					}
				}
			}
		}
	}
	if finals != 1 || observation.Journal[len(observation.Journal)-1].Kind != agentruntime.Finished {
		t.Fatalf("measurement has %d finals or did not finish", finals)
	}
}

type nativeReleaseBinding struct{ authority agenttools.Authority }

func (b nativeReleaseBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (agenttools.Authority, error) {
	return b.authority, nil
}

type nativeReleaseJournal struct {
	entries []agentruntime.Entry
	path    string
}

func (j *nativeReleaseJournal) Load(context.Context, string) ([]agentruntime.Entry, error) {
	return slices.Clone(j.entries), nil
}
func (j *nativeReleaseJournal) Append(_ context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	if expected != uint64(len(j.entries)) {
		return agentruntime.Entry{}, agentruntime.ErrConflict
	}
	entry := agentruntime.Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: slices.Clone(payload)}
	next := append(slices.Clone(j.entries), entry)
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return agentruntime.Entry{}, err
	}
	if err = os.WriteFile(j.path, append(data, '\n'), 0600); err != nil {
		return agentruntime.Entry{}, err
	}
	j.entries = next
	return entry, nil
}
