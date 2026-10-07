package agenttools

import (
	"strings"
	"testing"
)

func TestSymbolToolsFindDeclarationsAndSemanticReferences(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/symbols\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "provider.go", "package fixture\nfunc Export(value string) string { return value }\n")
	writeSnapshotFixture(t, root, "consumer.go", "package fixture\nfunc Use() string { return Export(\"ok\") }\n// Export is also in a comment.\nfunc Shadow() { Export := 1; _ = Export }\n")
	got, err := testHost().findSymbols(t.Context(), root, call("find_definitions", `{"symbol":"Export"}`))
	if err != nil || len(got.Definitions) != 1 || got.Definitions[0].Path != "provider.go" || got.Definitions[0].Line != 2 || !strings.Contains(got.Definitions[0].Signature, "value string") {
		t.Fatalf("declaration: %+v %v", got, err)
	}
	got, err = testHost().findSymbols(t.Context(), root, call("find_references", `{"path":"provider.go","line":2}`))
	if err != nil || len(got.References) != 1 || got.References[0].Path != "consumer.go" || got.References[0].Line != 2 || len(got.Unresolved) != 0 {
		t.Fatalf("references: %+v %v", got, err)
	}
}

func TestSymbolToolsReportUnresolvedImportsAndIgnoreExcludedFiles(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/symbols\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "provider.go", "package fixture\nfunc Export() {}\n")
	writeSnapshotFixture(t, root, "excluded.go", "//go:build nonexistent_platform\n\npackage fixture\nfunc Export() {}\n")
	writeSnapshotFixture(t, root, "external.go", "package fixture\nimport \"external.invalid/missing\"\nfunc Use() { Export(); missing.Unknown() }\n")
	got, err := testHost().findSymbols(t.Context(), root, call("find_definitions", `{"symbol":"Export"}`))
	if err != nil || len(got.Definitions) != 1 {
		t.Fatalf("build exclusion: %+v %v", got, err)
	}
	got, err = testHost().findSymbols(t.Context(), root, call("find_references", `{"path":"provider.go","line":2}`))
	if err != nil || len(got.Unresolved) == 0 {
		t.Fatalf("unresolved package concealed: %+v %v", got, err)
	}
}
