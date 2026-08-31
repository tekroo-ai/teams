package operatorhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const testToken = "0123456789abcdef0123456789abcdef"

func TestHandlerRequiresBearerAndRejectsBrowserOrigin(t *testing.T) {
	handler := newTestHandler(t, &operatorService{state: operationalruntime.ControlRunning}, func() {})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", response.Code)
	}
	request = authorizedRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Origin", "http://127.0.0.1:3000")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("origin status = %d", response.Code)
	}
}

func TestHandlerControlsRuntimeAndRequestsStop(t *testing.T) {
	service := &operatorService{state: operationalruntime.ControlRunning}
	stopped := 0
	handler := newTestHandler(t, service, func() { stopped++ })
	for _, item := range []struct {
		path  string
		state operationalruntime.ControlState
		code  int
	}{{"/v1/pause", operationalruntime.ControlPaused, http.StatusOK}, {"/v1/resume", operationalruntime.ControlRunning, http.StatusOK}, {"/v1/stop", operationalruntime.ControlRunning, http.StatusAccepted}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authorizedRequest(http.MethodPost, item.path, nil))
		if response.Code != item.code || service.state != item.state {
			t.Fatalf("%s status=%d state=%s", item.path, response.Code, service.state)
		}
	}
	if stopped != 1 {
		t.Fatalf("stop requests = %d", stopped)
	}
}

func TestHandlerSubmitsStrictCommandAndReadsProjections(t *testing.T) {
	service := &operatorService{state: operationalruntime.ControlRunning}
	handler := newTestHandler(t, service, func() {})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/commands", strings.NewReader(`{"unexpected":true}`)))
	if response.Code != http.StatusBadRequest || service.submissions != 0 {
		t.Fatalf("unknown command status=%d submissions=%d", response.Code, service.submissions)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/commands", strings.NewReader(`{}`)))
	if response.Code != http.StatusOK || service.submissions != 1 {
		t.Fatalf("command status=%d submissions=%d", response.Code, service.submissions)
	}
	for _, path := range []string{"/v1/tasks/00000000-0000-7000-8000-000000000001", "/v1/stories/00000000-0000-7000-8000-000000000002", "/v1/invocations/00000000-0000-7000-8000-000000000003"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, authorizedRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestHandlerDecodesCanonicalContractCommandShape(t *testing.T) {
	service := &operatorService{state: operationalruntime.ControlRunning}
	handler := newTestHandler(t, service, func() {})
	body := `{"contract_manifest":"tekroo.kernel.contracts/0.8.0","command_id":"00000000-0000-7000-8000-000000000001","command_type":"tekroo.command.story.create","command_version":"1.6.0","target":{"kind":"story","id":"00000000-0000-7000-8000-000000000002"},"authority":{"kind":"HUMAN","id":"principal"},"actor_fqn":null,"execution":null,"expected_revision":{"must_not_exist":true,"revision":0},"preconditions":[],"expected_lifecycle_epoch":null,"expected_policy_revision":1,"expected_catalogue_revision":8,"idempotency_key":"canonical-shape","correlation_id":"00000000-0000-7000-8000-000000000003","causation":[],"issued_at":null,"payload":{},"evidence_refs":[]}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/commands", strings.NewReader(body)))
	if response.Code != http.StatusOK || service.lastCommand.CommandType != "tekroo.command.story.create" || service.lastCommand.Target.Kind != kernel.AggregateStory {
		t.Fatalf("canonical command status=%d command=%#v", response.Code, service.lastCommand)
	}
}

func newTestHandler(t *testing.T, service Service, stop func()) *Handler {
	t.Helper()
	handler, err := NewHandler(Config{Service: service, BearerToken: testToken, OperationTimeout: time.Second, MaximumBodyBytes: 4096, RequestStop: stop})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func authorizedRequest(method, target string, body *strings.Reader) *http.Request {
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
	}
	request.Header.Set("Authorization", "Bearer "+testToken)
	return request
}

type operatorService struct {
	state       operationalruntime.ControlState
	submissions int
	lastCommand kernel.KernelCommand
}

func (service *operatorService) Status() operationalruntime.ControlStatus {
	return operationalruntime.ControlStatus{State: service.state}
}

func (service *operatorService) Pause(context.Context) error {
	if service.state != operationalruntime.ControlRunning {
		return operationalruntime.ErrControlConflict
	}
	service.state = operationalruntime.ControlPaused
	return nil
}

func (service *operatorService) Resume(context.Context) error {
	if service.state != operationalruntime.ControlPaused {
		return operationalruntime.ErrControlConflict
	}
	service.state = operationalruntime.ControlRunning
	return nil
}

func (service *operatorService) Submit(_ context.Context, command kernel.KernelCommand) (kernel.CommandReceipt, error) {
	service.submissions++
	service.lastCommand = command
	return kernel.CommandReceipt{}, nil
}

func (service *operatorService) ReadTask(context.Context, kernel.UUIDv7) (mongo.TaskProjection, bool, error) {
	return mongo.TaskProjection{Kind: "task_projection", ID: "00000000-0000-7000-8000-000000000001"}, true, nil
}

func (service *operatorService) ReadStory(context.Context, kernel.UUIDv7) (mongo.StoryProjection, bool, error) {
	return mongo.StoryProjection{Kind: "story_projection", ID: "00000000-0000-7000-8000-000000000002"}, true, nil
}

func (service *operatorService) ReadInvocation(context.Context, kernel.UUIDv7) (operationalruntime.InvocationStatus, bool, error) {
	return operationalruntime.InvocationStatus{InvocationID: "00000000-0000-7000-8000-000000000003", Revision: 2, State: kernel.InvocationStarted, LastEventID: "00000000-0000-7000-8000-000000000004"}, true, nil
}

func (service *operatorService) RoleRoster(context.Context) ([]organization.RoleInstanceState, error) {
	return []organization.RoleInstanceState{}, nil
}

func (service *operatorService) StartRole(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{ActorFQN: actor}, nil
}

func (service *operatorService) StopRole(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{ActorFQN: actor}, nil
}

func (service *operatorService) RestartRole(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{ActorFQN: actor}, nil
}

func (service *operatorService) PauseRole(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{ActorFQN: actor}, nil
}

func (service *operatorService) ResumeRole(_ context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	return organization.RoleInstanceState{ActorFQN: actor}, nil
}

func (service *operatorService) RoleInboxSnapshot(kernel.ActorFQN) []organization.OrganizationalMessage {
	return nil
}

func (service *operatorService) SendMessage(context.Context, organization.OrganizationalMessage) error {
	return nil
}

func (service *operatorService) ReadMessage(context.Context, kernel.UUIDv7) (organization.MessageClaim, bool, error) {
	return organization.MessageClaim{}, true, nil
}

func (service *operatorService) TraceMessages(context.Context, kernel.UUIDv7) ([]organization.MessageClaim, error) {
	return []organization.MessageClaim{}, nil
}

func (service *operatorService) DeadLetters(context.Context, kernel.ActorFQN, int64) ([]organization.MessageClaim, error) {
	return []organization.MessageClaim{}, nil
}

var _ Service = (*operatorService)(nil)
var _ OrganizationalService = (*operatorService)(nil)
