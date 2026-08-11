# Phase 3 Step 2 — HTTP/MCP adapter

**Authority:** Principal statement `Accepted. Proceed to the next step.` on
2026-08-11 after acceptance of Phase 3 Step 1.

## Accepted boundary

HTTP and MCP remain thin transports over the Step 1 typed command endpoint.
They do not import persistence, interpret model output, or create organizational
truth. Authentication is supplied by trusted server composition. A client body
cannot assert its principal, actor FQN, execution ID, or fencing epoch; the
adapter injects those values after authentication and the gateway checks their
exact equality with the command.

The HTTP command endpoint accepts one strict JSON invocation and returns one
typed response. The MCP endpoint implements the stateless, non-streaming JSON
response form of Streamable HTTP, tool discovery, and one
`tekroo.command.invoke` tool. Each request independently carries its protocol
version and client capabilities. There is no connection- or session-derived
identity, capability, or organizational state.

Origin validation, authentication, access-control composition, rate-limit
composition, bounded bodies, explicit protocol versions, and request timeouts
fail closed. HTTP disconnect/cancellation after command dispatch preserves the
gateway's uncertain-outcome semantics and never asserts rollback.

## Wire basis

The implementation targets the official MCP `2026-07-28` specification, which
is the revision reached by the official `latest` pointer on 2026-08-11:

- `https://modelcontextprotocol.io/specification/2026-07-28/basic`
- `https://modelcontextprotocol.io/specification/2026-07-28/server/discover`
- `https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http`
- `https://modelcontextprotocol.io/specification/2026-07-28/server/tools`

The selected Streamable HTTP profile returns one `application/json` JSON-RPC
object for each request. Required protocol, method, and tool-name HTTP headers
must exactly match the request body, including supported Base64 sentinel
decoding for `Mcp-Name`. GET and DELETE return `405 Method Not Allowed`.
Therefore SSE progress/subscription streams and multi-round-trip requests are
explicitly not claimed. Legacy initialization, protocol sessions, standalone
GET streams, and resumability are deliberately absent because the current
revision removed them.

Live OpenHands, SMA, providers, public deployment, migration, and performance
remain outside this authority.
