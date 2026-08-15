package eventexport_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/eventexport"
	"github.com/tekroo-ai/teams/kernel"
)

func TestExportPreservesExactBytesAndOpensLiveBeforeBacklog(t *testing.T) {
	source, _ := memory.NewEventExportSource("teams-main")
	raw := eventBytes(t, "00000000-0000-7000-8000-000000000101")
	if err := source.Append(raw); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	source.SetOpenObserver(func(value string) { order = append(order, value) })
	service := newService(t, source, time.Now, nil)
	response, err := service.Export(context.Background(), identity("sma"), request())
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != eventexport.StatusRecords || len(response.Records) != 1 {
		t.Fatalf("response = %#v", response)
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Records[0].EventBytesBase64)
	if err != nil || string(decoded) != string(raw) {
		t.Fatalf("exported bytes changed: %v", err)
	}
	digest := sha256.Sum256(raw)
	if response.Records[0].EventBytesSHA256 != kernel.Digest(hex.EncodeToString(digest[:])) || response.Records[0].EventBytesLength != len(raw) {
		t.Fatal("byte receipt mismatch")
	}
	if len(order) != 2 || order[0] != "LIVE_OPENED" || order[1] != "BACKLOG_SNAPSHOTTED" {
		t.Fatalf("open order = %#v", order)
	}
}

func TestCursorIsIntegritySourceAndPrincipalBound(t *testing.T) {
	source, _ := memory.NewEventExportSource("teams-main")
	if err := source.Append(eventBytes(t, "00000000-0000-7000-8000-000000000101")); err != nil {
		t.Fatal(err)
	}
	service := newService(t, source, time.Now, nil)
	first, err := service.Export(context.Background(), identity("sma"), request())
	if err != nil {
		t.Fatal(err)
	}
	next := request()
	next.Cursor = first.NextCursor
	if response, err := service.Export(context.Background(), identity("sma"), next); err != nil || response.Status != eventexport.StatusCaughtUp {
		t.Fatalf("resume = %#v, %v", response, err)
	}
	next.Cursor += "x"
	if _, err := service.Export(context.Background(), identity("sma"), next); !errors.Is(err, eventexport.ErrInvalidCursor) {
		t.Fatalf("tamper error = %v", err)
	}
	next.Cursor = first.NextCursor
	if _, err := service.Export(context.Background(), identity("other"), next); !errors.Is(err, eventexport.ErrUnauthorized) {
		t.Fatalf("principal error = %v", err)
	}
}

func TestHistoryLossIsExplicitResyncRequired(t *testing.T) {
	source, _ := memory.NewEventExportSource("teams-main")
	if err := source.Append(eventBytes(t, "00000000-0000-7000-8000-000000000101")); err != nil {
		t.Fatal(err)
	}
	service := newService(t, source, time.Now, nil)
	first, err := service.Export(context.Background(), identity("sma"), request())
	if err != nil {
		t.Fatal(err)
	}
	source.ResetHistory()
	next := request()
	next.Cursor = first.NextCursor
	response, err := service.Export(context.Background(), identity("sma"), next)
	if err != nil || response.Status != eventexport.StatusResyncRequired || response.NextCursor == "" {
		t.Fatalf("resync = %#v, %v", response, err)
	}
}

func TestAuthorizationBoundsExpiryAndContentFreeTelemetry(t *testing.T) {
	source, _ := memory.NewEventExportSource("teams-main")
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	observed := []eventexport.TelemetryRecord{}
	service := newService(t, source, func() time.Time { return now }, eventexport.TelemetryFunc(func(record eventexport.TelemetryRecord) { observed = append(observed, record) }))
	denied := request()
	denied.SourceID = "private"
	if _, err := service.Export(context.Background(), identity("sma"), denied); !errors.Is(err, eventexport.ErrUnauthorized) {
		t.Fatalf("authorization error = %v", err)
	}
	invalid := request()
	invalid.Limit = eventexport.MaximumLimit + 1
	if _, err := service.Export(context.Background(), identity("sma"), invalid); !errors.Is(err, eventexport.ErrInvalidRequest) {
		t.Fatalf("limit error = %v", err)
	}
	first, err := service.Export(context.Background(), identity("sma"), request())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	next := request()
	next.Cursor = first.NextCursor
	if _, err := service.Export(context.Background(), identity("sma"), next); !errors.Is(err, eventexport.ErrInvalidCursor) {
		t.Fatalf("expiry error = %v", err)
	}
	if len(observed) != 4 {
		t.Fatalf("telemetry count = %d", len(observed))
	}
	encoded, _ := json.Marshal(observed)
	if string(encoded) == "" || json.Valid(encoded) == false {
		t.Fatal("invalid telemetry")
	}
}

func newService(t *testing.T, source eventexport.Source, clock func() time.Time, telemetry eventexport.Telemetry) *eventexport.Service {
	t.Helper()
	service, err := eventexport.NewService(eventexport.Config{Source: source, Authorizer: eventexport.AuthorizerFunc(func(identity protocol.AuthenticatedContext, sourceID string) bool {
		return identity.Principal.ID == "sma" && sourceID == "teams-main"
	}), CursorKey: []byte("0123456789abcdef0123456789abcdef"), CursorTTL: time.Hour, MaximumLimit: 100, MaximumWait: time.Second, Clock: clock, Telemetry: telemetry})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func identity(id string) protocol.AuthenticatedContext {
	return protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: id}}
}

func request() eventexport.Request {
	return eventexport.Request{ProtocolVersion: eventexport.ProtocolVersion, RequestID: "00000000-0000-7000-8000-000000000001", SourceID: "teams-main", Limit: 10, WaitMillis: 100}
}

func eventBytes(t *testing.T, id kernel.UUIDv7) []byte {
	t.Helper()
	event := kernel.DomainEvent{ContractManifest: kernel.ContractIdentity, EventID: id, EventType: "tekroo.event.task.created", EventVersion: "1.6.0", Aggregate: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: "00000000-0000-7000-8000-000000000002"}, AggregateRevision: 1, LifecycleEpoch: 1, CommandID: "00000000-0000-7000-8000-000000000003", Authority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams"}, CommittedAt: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), Payload: json.RawMessage(`{"name":"exact bytes"}`), ProvenanceDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	bytes, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}
