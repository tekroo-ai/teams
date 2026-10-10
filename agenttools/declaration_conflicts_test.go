package agenttools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoDeclarationCheckRetainsR62NamingConflict(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "organization"), 0700); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "organization/federation.go", "package organization\ntype AliasBinding struct { RouteID string }\nvar aliasPattern = 1\n")
	for _, source := range []string{
		"package organization\ntype AliasBinding struct { Alias string; ActorFQN string; Revision uint64 }",
		"package organization\nvar aliasPattern = 2",
	} {
		err := CheckNewGoDeclarations(t.Context(), root, []ProposedGoSource{{Path: "organization/alias.go", Source: source}})
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "organization/federation.go:") {
			t.Fatalf("conflict not located: %v", err)
		}
	}
	if err := CheckNewGoDeclarations(t.Context(), root, []ProposedGoSource{{Path: "organization/alias.go", Source: "package organization\ntype ActorAliasBinding struct { Alias string }; var actorAliasPattern = 1"}}); err != nil {
		t.Fatal(err)
	}
}

func TestGoDeclarationCheckPackageAndReceiverNamespaces(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "one"), 0700); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "one/source.go", "package one\ntype Existing int\nfunc (Existing) Validate() bool {return true}\n")
	proposals := []ProposedGoSource{{Path: "one/new.go", Source: "package one\ntype New int\nfunc (New) Validate() bool"}, {Path: "two/new.go", Source: "package two\ntype Existing int"}}
	if err := CheckNewGoDeclarations(t.Context(), root, proposals); err != nil {
		t.Fatal(err)
	}
	proposals = append(proposals, ProposedGoSource{Path: "one/duplicate.go", Source: "package one\ntype New string"})
	if err := CheckNewGoDeclarations(t.Context(), root, proposals); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-task duplicate admitted: %v", err)
	}
	if err := CheckNewGoDeclarations(t.Context(), root, []ProposedGoSource{{Path: "../outside.go", Source: "package outside"}}); err == nil {
		t.Fatal("escape admitted")
	}
	if err := CheckNewGoDeclarations(t.Context(), root, []ProposedGoSource{{Path: "one/new.go", Source: "not Go"}}); err == nil {
		t.Fatal("invalid syntax certified")
	}
}
