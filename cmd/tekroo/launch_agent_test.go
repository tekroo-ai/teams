package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentDoesNotThrottleExplicitVerification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tekrood.plist")
	if err := writeLaunchAgent(path, "/test/bin/tekrood", "/test/config.json", "/test/state"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"<key>ProcessType</key><string>Interactive</string>", "<key>RunAtLoad</key><false/>", "<key>KeepAlive</key><false/>"} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("missing %s", required)
		}
	}
	if strings.Contains(string(data), "Background") {
		t.Fatal("child verification inherits background throttle")
	}
}
