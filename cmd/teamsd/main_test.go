package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRequiresExplicitConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(nil, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "usage: teamsd") || stdout.Len() != 0 {
		t.Fatalf("run error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestRunRejectsRelativeConfigurationPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"-config", "teamsd.json"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "configuration path must be absolute") || stdout.Len() != 0 {
		t.Fatalf("run error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}
