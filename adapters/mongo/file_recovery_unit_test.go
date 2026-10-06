package mongo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func retainedFileFixture() agenttools.EffectRecord {
	before := []byte("uncommitted source\r\n")
	sum := sha256.Sum256(before)
	return agenttools.EffectRecord{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000981"), RequestDigest: kernel.Digest(strings.Repeat("a", 64)), ToolCallID: "edit-1", Name: "edit_file", ArgumentsSHA256: strings.Repeat("b", 64), WorkspaceRoot: "/workspace", EffectPolicyHash: kernel.Digest(strings.Repeat("c", 64)), PreparedSHA256: strings.Repeat("d", 64), HasPreimage: true, FilePath: "source.go", BeforeExists: true, BeforeContent: before, BeforeSHA256: hex.EncodeToString(sum[:])}
}

func TestFilePreimageSurvivesBSONAndRejectsCorruption(t *testing.T) {
	record := retainedFileFixture()
	document, err := toolEffectCandidate(record)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := bson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var decoded toolEffectDocument
	if err := bson.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !sameEffectSnapshot(document, decoded) {
		t.Fatal("snapshot changed through BSON")
	}
	retained, err := retainedEffect(decoded)
	if err != nil || !bytes.Equal(retained.BeforeContent, record.BeforeContent) || retained.FilePath != record.FilePath || !retained.BeforeExists {
		t.Fatalf("retained: %+v %v", retained, err)
	}
	decoded.BeforeContent = []byte("wrong bytes")
	if _, err := retainedEffect(decoded); err == nil {
		t.Fatal("corrupt preimage accepted")
	}
	changed := document
	changed.BeforeExists = false
	if sameEffectSnapshot(document, changed) {
		t.Fatal("existence drift accepted")
	}
	changed = document
	changed.DesiredAbsent = true
	if sameEffectSnapshot(document, changed) {
		t.Fatal("removal drift accepted")
	}
}
