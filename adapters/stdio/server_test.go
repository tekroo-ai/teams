package stdio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/adapters/stdio"
	"github.com/tekroo-ai/teams/kernel"
)

type endpointFunc func(context.Context, protocol.Request) protocol.Response

func (function endpointFunc) Invoke(ctx context.Context, request protocol.Request) protocol.Response {
	return function(ctx, request)
}

func TestServerProcessesStrictFramesAndPreservesOneResponsePerFrame(t *testing.T) {
	request := minimalRequest()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server, err := stdio.NewServer(endpointFunc(func(_ context.Context, request protocol.Request) protocol.Response {
		calls++
		return protocol.Response{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: request.RequestID, Status: protocol.StatusReceipt, Receipt: &kernel.CommandReceipt{CommandID: request.Command.CommandID}}
	}), 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("{\n" + string(encoded) + "\n")
	var output bytes.Buffer
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var invalid, valid protocol.Response
	if err := decoder.Decode(&invalid); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&valid); err != nil {
		t.Fatal(err)
	}
	if invalid.Failure == nil || invalid.Failure.Code != protocol.FailureInvalidRequest || invalid.Failure.OutcomeUnknown {
		t.Fatalf("invalid response = %#v", invalid)
	}
	if valid.Status != protocol.StatusReceipt || valid.RequestID != request.RequestID || calls != 1 {
		t.Fatalf("valid response = %#v, calls=%d", valid, calls)
	}
}

func minimalRequest() protocol.Request {
	return protocol.Request{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: kernel.UUIDv7("00000000-0000-7000-8000-000000000004")}
}
