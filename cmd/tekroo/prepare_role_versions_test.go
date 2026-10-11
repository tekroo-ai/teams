package main

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestRolePreparationCLIRejectsInvalidArgumentsOffline(t *testing.T) {
	for _, args := range [][]string{
		{"prepare-role-versions"},
		{"prepare-role-versions", "--unknown"},
		{"prepare-role-versions", "unexpected-operand"},
		{"prepare-role-versions", "--config", "relative.json"},
		{"prepare-role-versions", "--config", filepath.Join(t.TempDir(), "missing.json")},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(args, bytes.NewReader(nil), &stdout, &stderr); err == nil {
			t.Fatalf("accepted %v", args)
		}
		if stdout.Len() != 0 {
			t.Fatal("failed preparation printed a success receipt")
		}
	}
}
