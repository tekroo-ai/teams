package protocol

import (
	"context"
)

// HTTPMCPEndpoint is the semantic command surface shared by future HTTP and MCP
// encodings. Those adapters may translate framing, never command meaning.
type HTTPMCPEndpoint interface {
	Invoke(context.Context, Request) Response
}
