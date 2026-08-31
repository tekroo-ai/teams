package mongo

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestDecodeRoleInstanceRejectsContradictoryEnvelope(t *testing.T) {
	state := validRoleInstanceState(t)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	document := roleInstanceDocument{ID: string(state.ActorFQN), Team: state.Team, Revision: state.Revision, Status: string(state.Status), Data: raw}
	if _, err := decodeRoleInstance(document); err != nil {
		t.Fatalf("decode valid role: %v", err)
	}

	mutations := []roleInstanceDocument{document, document, document, document}
	mutations[0].ID = "teams::coder-2"
	mutations[1].Team = "other"
	mutations[2].Revision++
	mutations[3].Status = string(organization.RoleFailed)
	for index, mutation := range mutations {
		if _, err := decodeRoleInstance(mutation); !errors.Is(err, organization.ErrRoleStateConflict) {
			t.Fatalf("mutation %d: got %v, want role state conflict", index, err)
		}
	}
}

func TestDecodeRoleInstanceRejectsUnknownAndTrailingData(t *testing.T) {
	state := validRoleInstanceState(t)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	document := roleInstanceDocument{ID: string(state.ActorFQN), Team: state.Team, Revision: state.Revision, Status: string(state.Status)}
	document.Data = append(raw[:len(raw)-1], []byte(`,"unexpected":true}`)...)
	if _, err := decodeRoleInstance(document); err == nil {
		t.Fatal("unknown persisted field accepted")
	}
	document.Data = append(raw, []byte(` {}`)...)
	if _, err := decodeRoleInstance(document); err == nil {
		t.Fatal("trailing document accepted")
	}
}

func validRoleInstanceState(t *testing.T) organization.RoleInstanceState {
	t.Helper()
	actor, err := kernel.ParseActorFQN("teams::coder-1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := kernel.ParseUUIDv7("018f25e0-7b3d-7abc-8def-0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	return organization.RoleInstanceState{
		ActorFQN: actor, Team: "teams", Role: "coder", Instance: 1, Revision: 1,
		BundleDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		ModelProfile: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		WorkspaceID:  "workspace-coder-1", Status: organization.RoleIdle,
		Execution: kernel.ExecutionTuple{ExecutionID: id, FencingEpoch: 1}, ProcessIdentity: "in-process:test",
		StartedAt: now, LastHeartbeatAt: now,
		ManifestDigest:  kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		ManifestVersion: "1.0.0", BundleVersion: "1.0.0", RuntimeGeneration: 1,
	}
}
