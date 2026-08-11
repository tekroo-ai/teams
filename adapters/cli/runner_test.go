package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/adapters/cli"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

type endpointFunc func(context.Context, protocol.Request) protocol.Response

func (function endpointFunc) Invoke(ctx context.Context, request protocol.Request) protocol.Response {
	return function(ctx, request)
}

func TestInvokeReturnsStableExitCodesAndJSONResponse(t *testing.T) {
	endpoint := endpointFunc(func(_ context.Context, request protocol.Request) protocol.Response {
		return protocol.Response{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: request.RequestID, Status: protocol.StatusReceipt, Receipt: &kernel.CommandReceipt{CommandID: request.Command.CommandID}}
	})
	runner, err := cli.NewDefaultRunner(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.Request{EnvelopeVersion: protocol.EnvelopeVersion, RequestID: kernel.UUIDv7("00000000-0000-7000-8000-000000000004")}
	input, _ := json.Marshal(request)
	var output bytes.Buffer
	if code := runner.Run(context.Background(), []string{"invoke"}, bytes.NewReader(input), &output); code != cli.ExitSuccess {
		t.Fatalf("exit code = %d, output = %s", code, output.String())
	}
	var response protocol.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != protocol.StatusReceipt || response.RequestID != request.RequestID {
		t.Fatalf("response = %#v", response)
	}
}

func TestInvokeRejectsMalformedInputWithoutCallingEndpoint(t *testing.T) {
	called := false
	endpoint := endpointFunc(func(context.Context, protocol.Request) protocol.Response {
		called = true
		return protocol.Response{}
	})
	runner, err := cli.NewDefaultRunner(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := runner.Run(context.Background(), []string{"invoke"}, bytes.NewBufferString("{"), &output); code != cli.ExitUsage {
		t.Fatalf("exit code = %d", code)
	}
	if called {
		t.Fatal("endpoint was called for malformed input")
	}
}
