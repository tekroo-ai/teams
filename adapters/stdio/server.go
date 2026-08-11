package stdio

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/tekroo-ai/teams/adapters/protocol"
)

const DefaultMaxFrameBytes = 1 << 20

var ErrInvalidConfiguration = errors.New("invalid stdio server configuration")

type Server struct {
	endpoint      protocol.Endpoint
	maxFrameBytes int
}

func NewServer(endpoint protocol.Endpoint, maxFrameBytes int) (*Server, error) {
	if endpoint == nil || maxFrameBytes <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &Server{endpoint: endpoint, maxFrameBytes: maxFrameBytes}, nil
}

func (server *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	if input == nil || output == nil {
		return ErrInvalidConfiguration
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), server.maxFrameBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		request, err := protocol.DecodeRequest(bytes.NewReader(scanner.Bytes()))
		if err != nil {
			response := protocol.Response{
				EnvelopeVersion: protocol.EnvelopeVersion,
				Status:          protocol.StatusInvocationFailure,
				Failure: &protocol.InvocationFailure{
					Code: protocol.FailureInvalidRequest, Message: err.Error(), OutcomeUnknown: false,
				},
			}
			if err := protocol.EncodeResponse(output, response); err != nil {
				return fmt.Errorf("write invalid-request response: %w", err)
			}
			continue
		}
		if err := protocol.EncodeResponse(output, server.endpoint.Invoke(ctx, request)); err != nil {
			return fmt.Errorf("write command response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdio frame: %w", err)
	}
	return ctx.Err()
}
