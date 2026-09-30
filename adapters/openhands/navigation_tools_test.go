package openhands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tekroo-ai/teams/adapters/filesystem"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestInvocationReadersBindEvidenceAndBaselineWithoutModelSelectedPaths(t *testing.T) {
	root := t.TempDir()
	store, err := filesystem.NewExecutionEvidenceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000123")
	receipt, err := store.Put(context.Background(), id, []byte("admitted evidence"))
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"tools": []any{map[string]any{"name": "file_read"}}}
	brief := application.ExecutionBrief{
		Evidence: []kernel.EvidenceRef{{EvidenceID: id, SHA256: receipt.SHA256}},
		Scope:    kernel.TaskOperationalScope{BaselineSHA: "1111111111111111111111111111111111111111"},
	}
	bound, err := withInvocationReadTools(context.Background(), settings, brief, root, store)
	if err != nil {
		t.Fatal(err)
	}
	tools := bound.(map[string]any)["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("bound tools = %#v", tools)
	}
	evidence := tools[1].(map[string]any)
	if evidence["name"] != "read_evidence" || evidence["params"].(map[string]any)["evidence_root"] != root {
		t.Fatalf("evidence binding = %#v", evidence)
	}
	diff := tools[2].(map[string]any)
	if diff["name"] != "repository_diff_operations" || diff["params"].(map[string]any)["baseline_commit"] != brief.Scope.BaselineSHA {
		t.Fatalf("diff binding = %#v", diff)
	}
}

func TestInvocationDoesNotExposeFileReaderForMetadataOnlyEvidence(t *testing.T) {
	root := t.TempDir()
	store, err := filesystem.NewExecutionEvidenceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"tools": []any{map[string]any{"name": "file_read"}}}
	brief := application.ExecutionBrief{Evidence: []kernel.EvidenceRef{{
		EvidenceID: "00000000-0000-7000-8000-000000000123",
		SHA256:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}}
	bound, err := withInvocationReadTools(context.Background(), settings, brief, root, store)
	if err != nil {
		t.Fatal(err)
	}
	tools := bound.(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "file_read" {
		t.Fatalf("metadata-only evidence exposed a file reader: %#v", tools)
	}
}

func TestSinglePurposeNavigationEventsKeepRepositoryEvidenceSemantics(t *testing.T) {
	read := rawEvent{ToolName: "file_read", ActionPath: "src/main.go", ActionCommand: "view", ActionPayload: json.RawMessage(`{"path":"src/main.go","start_line":10,"end_line":20}`)}
	listed := rawEvent{ToolName: "list_files", ActionPayload: json.RawMessage(`{"directory":"src"}`)}
	found := rawEvent{ToolName: "find_files", ActionPayload: json.RawMessage(`{"filename_glob":"*.go","directory":"src"}`)}
	searched := rawEvent{ToolName: "search_file_contents", ActionPath: "src", ActionPayload: json.RawMessage(`{"regex":"Widget","path":"src"}`)}

	if !repositoryAction(read) || !repositoryInspectionAction(read) || !repositoryContentReadAction(read) || repositoryFileListingAction(read) {
		t.Fatal("file_read must be a content read, not a listing")
	}
	if _, ok := repositoryViewRange(read); !ok {
		t.Fatal("file_read scalar range must be inspected")
	}
	for _, event := range []rawEvent{listed, found} {
		if !repositoryAction(event) || !repositoryFileListingAction(event) || repositoryContentReadAction(event) {
			t.Fatalf("%s must be a listing, not a content read", event.ToolName)
		}
	}
	if !repositorySearchTool(searched.ToolName) || !repositoryContentReadAction(searched) {
		t.Fatal("search_file_contents must be recognized as content search")
	}
	if !checkpointActionIsRepositoryEvidence(checkpointAction{Tool: "file_read"}) || !checkpointActionIsRepositoryEvidence(checkpointAction{Tool: "search_file_contents"}) {
		t.Fatal("new read/search tools must count as checkpoint evidence")
	}
}

func TestNavigationProfilesRejectLegacyModernMix(t *testing.T) {
	if validExplicitAgentTools([]string{"repository_view", "file_read"}) {
		t.Fatal("legacy and new navigation tools must not coexist in one profile")
	}
	if !validExplicitAgentTools([]string{"command_operations", "file_read", "list_files", "find_files", "search_file_contents", "file_write_commands", "checklist_operations"}) {
		t.Fatal("new implementation tool surface must be accepted")
	}
	if !validExplicitAgentTools([]string{"terminal", "glob", "repository_search", "file_editor_commands", "task_tracker"}) {
		t.Fatal("historical tool surface must remain loadable")
	}
}

func TestEveryStarterRoleHasCapabilityAppropriateTools(t *testing.T) {
	teamRoot := filepath.Join("..", "..", "config", "starter-team")
	teamBytes, err := os.ReadFile(filepath.Join(teamRoot, "team.v4.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var team struct {
		Roles []struct {
			Role       string `json:"role"`
			BundlePath string `json:"bundle_path"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(teamBytes, &team); err != nil {
		t.Fatal(err)
	}
	read := []string{"file_read", "list_files", "find_files", "search_file_contents"}
	check := append([]string{"command_operations"}, read...)
	edit := append(append([]string(nil), check...), "file_write_commands", "checklist_operations")
	want := map[string][]string{
		"architect":       read,
		"coder":           edit,
		"operator":        {},
		"product-owner":   read,
		"project-manager": read,
		"security":        check,
		"senior-coder":    edit,
		"tester":          check,
	}
	if len(team.Roles) != len(want) {
		t.Fatalf("starter roles=%d, audited roles=%d", len(team.Roles), len(want))
	}
	for _, role := range team.Roles {
		expected, audited := want[role.Role]
		if !audited {
			t.Errorf("role %q has no tool-coverage audit", role.Role)
			continue
		}
		bundleBytes, err := os.ReadFile(filepath.Join(teamRoot, role.BundlePath))
		if err != nil {
			t.Fatal(err)
		}
		var bundle struct {
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(bundleBytes, &bundle); err != nil {
			t.Fatal(err)
		}
		actual := ExecutionToolsForPermissions(bundle.Permissions)
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("role %q: tools=%v, want %v", role.Role, actual, expected)
		}
		delete(want, role.Role)
	}
}

func TestEditorialExampleDoesNotImplicitlyGainRepositoryTools(t *testing.T) {
	bundlePath := filepath.Join("..", "..", "config", "editorial-team", "roles-v4", "editor", "role.json")
	bundleBytes, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(bundleBytes, &bundle); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bundle.Permissions, []string{"artifact.read", "artifact.write"}) {
		t.Fatalf("editor permissions changed; re-audit its tool needs: %v", bundle.Permissions)
	}
	if tools := ExecutionToolsForPermissions(bundle.Permissions); len(tools) != 0 {
		t.Fatalf("editorial artifact authority must not imply repository authority: %v", tools)
	}
}
