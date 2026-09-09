package organization

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/kernel"
)

func TestStarterTeamRoleHostReplacementAcceptance(t *testing.T) {
	manifestPath, err := filepath.Abs(filepath.Join("..", "config", "starter-team", "team.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	publicRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil {
		t.Fatal(err)
	}
	groundingRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "role-grounding-publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	groundingKey, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(groundingRaw)))
	if err != nil {
		t.Fatal(err)
	}
	team, err := LoadTeamManifest(manifestPath, kernel.Digest("3478f27988da4f7c022df0ca7145af88b8e8cc1eb6fd446402ff69b33519c693"), map[string]ed25519.PublicKey{"tekroo-phase6-bootstrap": publicKey, "tekroo-role-grounding-20260901": groundingKey})
	if err != nil {
		t.Fatal(err)
	}
	worker := &acceptanceWorker{active: make(map[kernel.ActorFQN]kernel.ExecutionTuple), started: make(chan StartRoleRequest, 3)}
	runtime, err := NewInProcessRuntime(worker)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(team, NewMemoryRoleStore(), runtime, fake.NewClock(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)), fake.NewIDSource(
		kernel.UUIDv7("00000000-0000-7000-8000-000000001101"),
		kernel.UUIDv7("00000000-0000-7000-8000-000000001102"),
		kernel.UUIDv7("00000000-0000-7000-8000-000000001103"),
	))
	if err != nil {
		t.Fatal(err)
	}
	eager, err := host.StartEager(context.Background())
	if err != nil || len(eager) != 1 || eager[0].ActorFQN != "example::product-owner-1" {
		t.Fatalf("eager roles=%+v err=%v", eager, err)
	}
	<-worker.started
	coder := kernel.ActorFQN("example::coder-1")
	first, err := host.Start(context.Background(), coder)
	if err != nil {
		t.Fatal(err)
	}
	<-worker.started
	second, err := host.Restart(context.Background(), coder)
	if err != nil {
		t.Fatal(err)
	}
	<-worker.started
	if second.ActorFQN != first.ActorFQN || second.WorkspaceID != first.WorkspaceID || second.Execution.FencingEpoch != first.Execution.FencingEpoch+1 {
		t.Fatalf("replacement lost continuity: first=%+v second=%+v", first, second)
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.active) != 2 || worker.active[coder] != second.Execution {
		t.Fatalf("active work duplicated or stale: %+v", worker.active)
	}
}

type acceptanceWorker struct {
	mu      sync.Mutex
	active  map[kernel.ActorFQN]kernel.ExecutionTuple
	started chan StartRoleRequest
}

func (worker *acceptanceWorker) Run(ctx context.Context, request StartRoleRequest) error {
	worker.mu.Lock()
	worker.active[request.ActorFQN] = request.Execution
	worker.mu.Unlock()
	worker.started <- request
	<-ctx.Done()
	worker.mu.Lock()
	if worker.active[request.ActorFQN] == request.Execution {
		delete(worker.active, request.ActorFQN)
	}
	worker.mu.Unlock()
	return ctx.Err()
}
