package protocol

import (
	"context"

	"github.com/tekroo-ai/teams/kernel"
)

// HTTPMCPEndpoint is the semantic command surface shared by future HTTP and MCP
// encodings. Those adapters may translate framing, never command meaning.
type HTTPMCPEndpoint interface {
	Invoke(context.Context, Request) Response
}

type DirectedRequest struct {
	Recipient kernel.ActorFQN `json:"recipient"`
	Request   Request         `json:"request"`
}

// ChannelEndpoint preserves exact directed routing without treating delivery as
// durable task ownership.
type ChannelEndpoint interface {
	Deliver(context.Context, DirectedRequest) Response
}
