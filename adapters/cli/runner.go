package cli

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/tekroo-ai/teams/adapters/daemon"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/adapters/stdio"
)

const (
	ExitSuccess           = 0
	ExitOperationalError  = 1
	ExitInvocationFailure = 2
	ExitUsage             = 64
)

var ErrInvalidConfiguration = errors.New("invalid CLI runner configuration")

type Runner struct {
	endpoint protocol.Endpoint
	host     *daemon.Host
}

func NewRunner(endpoint protocol.Endpoint, host *daemon.Host) (*Runner, error) {
	if endpoint == nil || host == nil {
		return nil, ErrInvalidConfiguration
	}
	return &Runner{endpoint: endpoint, host: host}, nil
}

func NewDefaultRunner(endpoint protocol.Endpoint) (*Runner, error) {
	server, err := stdio.NewServer(endpoint, stdio.DefaultMaxFrameBytes)
	if err != nil {
		return nil, err
	}
	host, err := daemon.NewHost(server, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return NewRunner(endpoint, host)
}

func (runner *Runner) Run(ctx context.Context, arguments []string, input io.Reader, output io.Writer) int {
	if len(arguments) != 1 || input == nil || output == nil {
		return ExitUsage
	}
	switch arguments[0] {
	case "invoke":
		request, err := protocol.DecodeRequest(input)
		if err != nil {
			_ = protocol.EncodeResponse(output, protocol.Response{
				EnvelopeVersion: protocol.EnvelopeVersion,
				Status:          protocol.StatusInvocationFailure,
				Failure: &protocol.InvocationFailure{
					Code: protocol.FailureInvalidRequest, Message: err.Error(), OutcomeUnknown: false,
				},
			})
			return ExitUsage
		}
		response := runner.endpoint.Invoke(ctx, request)
		if err := protocol.EncodeResponse(output, response); err != nil {
			return ExitOperationalError
		}
		if response.Status == protocol.StatusReceipt {
			return ExitSuccess
		}
		return ExitInvocationFailure
	case "serve-stdio":
		closer, ok := input.(io.ReadCloser)
		if !ok {
			closer = io.NopCloser(input)
		}
		if err := runner.host.Run(ctx, closer, output); err != nil && !errors.Is(err, context.Canceled) {
			return ExitOperationalError
		}
		return ExitSuccess
	default:
		return ExitUsage
	}
}
