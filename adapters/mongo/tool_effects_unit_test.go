package mongo

import (
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestEditEffectPreparedHashSurvivesBSONAndLegacyRecords(t *testing.T) {
	record := agenttools.EffectRecord{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000981"),
		RequestDigest: kernel.Digest(strings.Repeat("a", 64)), ToolCallID: "edit-1", Name: "edit_file",
		ArgumentsSHA256: strings.Repeat("b", 64), WorkspaceRoot: "/workspace", EffectPolicyHash: kernel.Digest(strings.Repeat("c", 64)),
		PreparedSHA256: strings.Repeat("d", 64)}
	document, err := toolEffectCandidate(record)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var decoded toolEffectDocument
	if err := bson.Unmarshal(encoded, &decoded); err != nil || decoded.PreparedSHA256 != record.PreparedSHA256 || !sameEffectIdentity(document, decoded) {
		t.Fatalf("prepared edit hash lost: %+v %v", decoded, err)
	}
	for _, bad := range []string{"short", strings.Repeat("G", 64), strings.Repeat("A", 64)} {
		record.PreparedSHA256 = bad
		if _, err := toolEffectCandidate(record); err == nil {
			t.Fatalf("invalid prepared hash accepted: %q", bad)
		}
	}
	record.Name, record.PreparedSHA256 = "write_file", ""
	legacy, err := toolEffectCandidate(record)
	if err != nil {
		t.Fatalf("legacy record rejected: %v", err)
	}
	encoded, err = bson.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bson.Raw(encoded).Lookup("prepared_sha256").Type != 0 {
		t.Fatal("legacy record gained a required field")
	}
}
