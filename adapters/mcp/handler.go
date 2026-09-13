package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/adapters/protocol"
)

const (
	ProtocolVersion        = "2026-07-28"
	ProtocolVersionHeader  = "MCP-Protocol-Version"
	MethodHeader           = "Mcp-Method"
	NameHeader             = "Mcp-Name"
	CommandToolName        = "tekroo.command.invoke"
	RolesListToolName      = "tekroo.roles.list"
	RoleControlToolName    = "tekroo.role.control"
	MessageSendToolName    = "tekroo.message.send"
	MessageGetToolName     = "tekroo.message.get"
	MessageTraceToolName   = "tekroo.message.trace"
	DeadLettersToolName    = "tekroo.deadletters.list"
	FeatureSubmitToolName  = "tekroo.feature.submit"
	FeatureGetToolName     = "tekroo.feature.get"
	FeatureTimingToolName  = "tekroo.feature.timing.get"
	FeaturePlanToolName    = "tekroo.feature.plan.apply"
	FeatureAcceptToolName  = "tekroo.feature.accept"
	FeatureReleaseToolName = "tekroo.feature.release"
	HumanRegisterToolName  = "tekroo.human.participant.register"
	HumanAskToolName       = "tekroo.human.question.ask"
	HumanNotificationsName = "tekroo.human.notifications.list"
	HumanRespondToolName   = "tekroo.human.response.record"
	HumanInteractionName   = "tekroo.human.interaction.get"
	StatusToolName         = "tekroo.status.get"
	TaskGetToolName        = "tekroo.task.get"
	StoryGetToolName       = "tekroo.story.get"
	InvocationGetToolName  = "tekroo.invocation.get"
	EventWaitToolName      = "tekroo.event.wait"
	LibrariesListToolName  = "tekroo.libraries.list"
	LibrariesSyncToolName  = "tekroo.libraries.sync"
	DiagnosticsToolName    = "tekroo.diagnostics.get"
	DeadLetterRepairName   = "tekroo.deadletter.repair"
	InvocationCancelName   = "tekroo.invocation.cancel"
	FederationInspectName  = "tekroo.federation.inspect"
	FederationResolveName  = "tekroo.federation.alias.resolve"
	FederationSendName     = "tekroo.federation.message.send"
	DefaultMaxBodyBytes    = int64(1 << 20)
	codeHeaderMismatch     = -32020
	codeUnsupportedVersion = -32022
)

var ErrInvalidConfiguration = errors.New("invalid MCP adapter configuration")

type Handler struct {
	endpoint      protocol.Endpoint
	focused       FocusedTools
	authenticator httpapi.Authenticator
	origins       httpapi.OriginPolicy
	limiter       httpapi.RateLimiter
	maxBodyBytes  int64
}

type FocusedTools interface {
	CallTool(context.Context, protocol.AuthenticatedContext, string, json.RawMessage) (any, error)
}

func NewHandler(endpoint protocol.Endpoint, authenticator httpapi.Authenticator, origins httpapi.OriginPolicy, limiter httpapi.RateLimiter, maxBodyBytes int64) (*Handler, error) {
	if endpoint == nil || authenticator == nil || origins == nil || limiter == nil || maxBodyBytes <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &Handler{endpoint: endpoint, authenticator: authenticator, origins: origins, limiter: limiter, maxBodyBytes: maxBodyBytes}, nil
}

func NewFocusedHandler(endpoint protocol.Endpoint, focused FocusedTools, authenticator httpapi.Authenticator, origins httpapi.OriginPolicy, limiter httpapi.RateLimiter, maxBodyBytes int64) (*Handler, error) {
	handler, err := NewHandler(endpoint, authenticator, origins, limiter, maxBodyBytes)
	if err != nil || focused == nil {
		return nil, ErrInvalidConfiguration
	}
	handler.focused = focused
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	secureHeaders(writer)
	origin := request.Header.Get("Origin")
	if origin != "" && !handler.origins.AllowOrigin(origin) {
		handler.writeError(writer, http.StatusForbidden, nil, codeInvalidRequest, "origin is not allowed", nil)
		return
	}
	identity, err := handler.authenticator.Authenticate(request)
	if err != nil || !identity.Valid() {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		handler.writeError(writer, http.StatusUnauthorized, nil, codeInvalidRequest, "authentication failed", nil)
		return
	}
	if !handler.limiter.Allow(identity) {
		handler.writeError(writer, http.StatusTooManyRequests, nil, codeInvalidRequest, "rate limit exceeded", nil)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		handler.writeError(writer, http.StatusMethodNotAllowed, nil, codeInvalidRequest, "method must be POST", nil)
		return
	}
	if !hasJSONContentType(request.Header.Get("Content-Type")) {
		handler.writeError(writer, http.StatusUnsupportedMediaType, nil, codeInvalidRequest, "content type must be application/json", nil)
		return
	}
	if !accepts(request.Header.Get("Accept"), "application/json") || !accepts(request.Header.Get("Accept"), "text/event-stream") {
		handler.writeError(writer, http.StatusNotAcceptable, nil, codeInvalidRequest, "Accept must include application/json and text/event-stream", nil)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, handler.maxBodyBytes)
	message, err := decodeMessage(request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			handler.writeError(writer, http.StatusRequestEntityTooLarge, nil, codeInvalidRequest, "request body exceeds configured bound", nil)
			return
		}
		handler.writeError(writer, http.StatusBadRequest, nil, codeParseError, "invalid JSON-RPC message", nil)
		return
	}
	if message.JSONRPC != "2.0" || message.Method == "" || !validRequestID(message.ID) || len(message.Result) != 0 || len(message.Error) != 0 {
		handler.writeError(writer, http.StatusBadRequest, message.ID, codeInvalidRequest, "invalid JSON-RPC request", nil)
		return
	}
	metadata, err := parseMetadata(message.Params)
	if err != nil {
		handler.writeError(writer, http.StatusBadRequest, message.ID, codeInvalidParams, "missing or invalid per-request MCP metadata", nil)
		return
	}
	if !handler.validateHeaders(writer, request, message, metadata) {
		return
	}
	switch message.Method {
	case "ping":
		handler.writeResult(writer, message.ID, completeResult(nil))
	case "server/discover":
		handler.discover(writer, message)
	case "tools/list":
		handler.listTools(writer, message)
	case "tools/call":
		handler.callTool(writer, request, message, identity)
	default:
		handler.writeError(writer, http.StatusNotFound, message.ID, codeMethodNotFound, "method not found", nil)
	}
}

func (handler *Handler) discover(writer http.ResponseWriter, message message) {
	var params struct {
		Meta map[string]any `json:"_meta"`
	}
	if err := decodeStrict(message.Params, &params); err != nil {
		handler.writeError(writer, http.StatusOK, message.ID, codeInvalidParams, "invalid server/discover parameters", nil)
		return
	}
	handler.writeResult(writer, message.ID, completeResult(map[string]any{
		"supportedVersions": []string{ProtocolVersion},
		"capabilities":      map[string]any{"tools": map[string]any{}},
		"instructions":      "Submit authenticated, idempotent Tekroo organizational commands and reconcile uncertain outcomes by command ID.",
		"cacheScope":        "private",
	}))
}

type requestMetadata struct {
	ProtocolVersion    string
	ClientCapabilities map[string]any
}

func parseMetadata(raw json.RawMessage) (requestMetadata, error) {
	var params map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &params) != nil || params == nil {
		return requestMetadata{}, errors.New("params must be an object")
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(params["_meta"], &metadata) != nil || metadata == nil {
		return requestMetadata{}, errors.New("_meta must be an object")
	}
	var result requestMetadata
	if json.Unmarshal(metadata["io.modelcontextprotocol/protocolVersion"], &result.ProtocolVersion) != nil || result.ProtocolVersion == "" {
		return requestMetadata{}, errors.New("protocol version is required")
	}
	if json.Unmarshal(metadata["io.modelcontextprotocol/clientCapabilities"], &result.ClientCapabilities) != nil || result.ClientCapabilities == nil {
		return requestMetadata{}, errors.New("client capabilities are required")
	}
	return result, nil
}

func (handler *Handler) validateHeaders(writer http.ResponseWriter, request *http.Request, message message, metadata requestMetadata) bool {
	headerVersion := request.Header.Get(ProtocolVersionHeader)
	if headerVersion == "" {
		handler.writeError(writer, http.StatusBadRequest, message.ID, codeHeaderMismatch, "missing MCP-Protocol-Version header", nil)
		return false
	}
	if headerVersion != ProtocolVersion {
		handler.writeError(writer, http.StatusBadRequest, message.ID, codeUnsupportedVersion, "unsupported MCP protocol version", map[string]any{"supported": []string{ProtocolVersion}, "requested": headerVersion})
		return false
	}
	if metadata.ProtocolVersion != headerVersion {
		handler.writeError(writer, http.StatusBadRequest, message.ID, codeHeaderMismatch, "protocol version header does not match request metadata", nil)
		return false
	}
	if request.Header.Get(MethodHeader) != message.Method {
		handler.writeError(writer, http.StatusBadRequest, message.ID, codeHeaderMismatch, "Mcp-Method header does not match request method", nil)
		return false
	}
	if message.Method == "tools/call" {
		name, err := toolName(message.Params)
		if err != nil {
			handler.writeError(writer, http.StatusBadRequest, message.ID, codeInvalidParams, "invalid tools/call parameters", nil)
			return false
		}
		headerName, err := decodeMirroredHeader(request.Header.Get(NameHeader))
		if err != nil || headerName != name {
			handler.writeError(writer, http.StatusBadRequest, message.ID, codeHeaderMismatch, "Mcp-Name header does not match request tool name", nil)
			return false
		}
	} else if request.Header.Get(NameHeader) != "" {
		handler.writeError(writer, http.StatusBadRequest, message.ID, codeHeaderMismatch, "Mcp-Name header is not valid for this method", nil)
		return false
	}
	return true
}

func (handler *Handler) listTools(writer http.ResponseWriter, message message) {
	var params struct {
		Cursor string         `json:"cursor,omitempty"`
		Meta   map[string]any `json:"_meta"`
	}
	if err := decodeStrict(message.Params, &params); err != nil || params.Cursor != "" {
		handler.writeError(writer, http.StatusOK, message.ID, codeInvalidParams, "invalid tools/list parameters", nil)
		return
	}
	tools := []any{commandTool()}
	if handler.focused != nil {
		tools = append(tools, organizationalTools()...)
	}
	handler.writeResult(writer, message.ID, completeResult(map[string]any{"tools": tools, "cacheScope": "private"}))
}

func (handler *Handler) callTool(writer http.ResponseWriter, request *http.Request, message message, identity protocol.AuthenticatedContext) {
	var params struct {
		Name           string                     `json:"name"`
		Arguments      json.RawMessage            `json:"arguments"`
		InputResponses map[string]json.RawMessage `json:"inputResponses,omitempty"`
		RequestState   json.RawMessage            `json:"requestState,omitempty"`
		Meta           map[string]any             `json:"_meta"`
	}
	if err := decodeStrict(message.Params, &params); err != nil || params.Name == "" || len(params.Arguments) == 0 {
		handler.writeError(writer, http.StatusOK, message.ID, codeInvalidParams, "invalid tools/call parameters", nil)
		return
	}
	if params.Name != CommandToolName {
		if params.Name == EventWaitToolName {
			// The focused wait operation carries its own bounded deadline.
			_ = http.NewResponseController(writer).SetWriteDeadline(time.Time{})
		}
		handler.callFocusedTool(writer, request, message, identity, params.Name, params.Arguments)
		return
	}
	invocation, err := protocol.DecodeInvocation(bytes.NewReader(params.Arguments))
	if err != nil {
		handler.writeError(writer, http.StatusOK, message.ID, codeInvalidParams, "invalid command invocation", nil)
		return
	}
	commandResponse := handler.endpoint.Invoke(request.Context(), invocation.Authenticate(identity))
	encoded, err := json.Marshal(commandResponse)
	if err != nil {
		handler.writeError(writer, http.StatusInternalServerError, message.ID, codeInternalError, "could not encode tool result", nil)
		return
	}
	result := completeResult(map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(encoded)}},
		"structuredContent": commandResponse,
		"isError":           commandResponse.Status != protocol.StatusReceipt,
	})
	handler.writeResult(writer, message.ID, result)
}

func (handler *Handler) callFocusedTool(writer http.ResponseWriter, request *http.Request, message message, identity protocol.AuthenticatedContext, name string, arguments json.RawMessage) {
	if handler.focused == nil {
		handler.writeError(writer, http.StatusOK, message.ID, codeInvalidParams, "unknown tool", map[string]any{"name": name})
		return
	}
	result, err := handler.focused.CallTool(request.Context(), identity, name, arguments)
	if err != nil {
		handler.writeError(writer, http.StatusOK, message.ID, codeInvalidParams, err.Error(), nil)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		handler.writeError(writer, http.StatusInternalServerError, message.ID, codeInternalError, "could not encode tool result", nil)
		return
	}
	handler.writeResult(writer, message.ID, completeResult(map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(encoded)}},
		"structuredContent": result,
		"isError":           false,
	}))
}

func completeResult(fields map[string]any) map[string]any {
	result := map[string]any{
		"resultType": "complete",
		"_meta": map[string]any{
			"io.modelcontextprotocol/serverInfo": map[string]any{"name": "tekroo-teams", "version": "4-phase3"},
		},
	}
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func toolName(raw json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &params) != nil || params.Name == "" {
		return "", errors.New("tool name is required")
	}
	return params.Name, nil
}

func decodeMirroredHeader(value string) (string, error) {
	if value == "" {
		return "", errors.New("header is required")
	}
	if strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?=") {
		encoded := strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?=")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	}
	if strings.TrimSpace(value) != value {
		return "", errors.New("unsafe plain header value")
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return "", errors.New("unsafe plain header value")
		}
	}
	return value, nil
}

func (handler *Handler) writeResult(writer http.ResponseWriter, id json.RawMessage, result any) {
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (handler *Handler) writeError(writer http.ResponseWriter, status int, id json.RawMessage, code int, message string, data any) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response{JSONRPC: "2.0", ID: id, Error: &responseError{Code: code, Message: message, Data: data}})
}

func commandTool() map[string]any {
	return map[string]any{
		"name":        CommandToolName,
		"title":       "Invoke Tekroo organizational command",
		"description": "Submit one authenticated, idempotent command to the Tekroo organizational kernel.",
		"inputSchema": map[string]any{
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"envelope_version", "request_id", "timeout_millis", "command", "provenance"},
			"properties": map[string]any{
				"envelope_version": map[string]any{"type": "string", "const": protocol.EnvelopeVersion},
				"request_id":       map[string]any{"type": "string"},
				"timeout_millis":   map[string]any{"type": "integer", "minimum": 1},
				"command":          map[string]any{"type": "object"},
				"provenance":       map[string]any{"type": "object"},
			},
		},
	}
}

func organizationalTools() []any {
	object := func(required []string, properties map[string]any) map[string]any {
		return map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "additionalProperties": false, "required": required, "properties": properties}
	}
	actor := map[string]any{"type": "string", "description": "Exact team::role-instance FQN."}
	uuid := map[string]any{"type": "string", "format": "uuid"}
	return []any{
		map[string]any{"name": FeatureSubmitToolName, "title": "Submit feature request", "description": "Submit one bounded feature request to the configured product owner.", "inputSchema": map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}},
		map[string]any{"name": FeatureGetToolName, "title": "Inspect feature request", "description": "Read the canonical feature workflow and finite plan.", "inputSchema": object([]string{"feature_id"}, map[string]any{"feature_id": uuid})},
		map[string]any{"name": FeatureTimingToolName, "title": "Inspect feature timing", "description": "Measure design, implementation, validation, queue, and parallel execution timing from durable workflow evidence.", "inputSchema": object([]string{"feature_id"}, map[string]any{"feature_id": uuid})},
		map[string]any{"name": FeaturePlanToolName, "title": "Apply reviewed feature plan", "description": "Materialize a finite reviewed story/task DAG.", "inputSchema": map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}},
		map[string]any{"name": FeatureAcceptToolName, "title": "Accept completed feature without a code release", "description": "Apply the submitting human's acceptance to a completed, independently reviewed feature and record an explicit reason that no repository release is required.", "inputSchema": object([]string{"feature_id", "expected_revision", "no_release_reason"}, map[string]any{"feature_id": uuid, "expected_revision": map[string]any{"type": "integer", "minimum": 1}, "no_release_reason": map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}})},
		map[string]any{"name": FeatureReleaseToolName, "title": "Release and accept completed feature", "description": "Execute the exact qualified FF-only release plan, verify authoritative Git state, and accept the feature.", "inputSchema": map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}},
		map[string]any{"name": HumanRegisterToolName, "title": "Register human participant", "description": "Bind an exact authenticated human participant to a scoped role and delivery channel.", "inputSchema": map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}},
		map[string]any{"name": HumanAskToolName, "title": "Ask a human participant", "description": "Deliver one bounded question to an exact participant and block the subject task until its authenticated response.", "inputSchema": map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}},
		map[string]any{"name": HumanNotificationsName, "title": "List my human notifications", "description": "List durable questions addressed to the authenticated human participant.", "inputSchema": object(nil, map[string]any{"open_only": map[string]any{"type": "boolean"}})},
		map[string]any{"name": HumanRespondToolName, "title": "Respond to human question", "description": "Record the authenticated participant's response and resume the exact blocked task.", "inputSchema": map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}},
		map[string]any{"name": HumanInteractionName, "title": "Inspect human interaction", "description": "Read one canonical human-interaction aggregate.", "inputSchema": object([]string{"interaction_id"}, map[string]any{"interaction_id": uuid})},
		map[string]any{"name": StatusToolName, "title": "Inspect team runtime", "description": "Read current tekrood control and liveness state.", "inputSchema": object(nil, map[string]any{})},
		map[string]any{"name": TaskGetToolName, "title": "Inspect task", "description": "Read one canonical task projection, assignment, scope, budget, and latest invocation.", "inputSchema": object([]string{"task_id"}, map[string]any{"task_id": uuid})},
		map[string]any{"name": StoryGetToolName, "title": "Inspect story", "description": "Read one canonical story projection and its task DAG.", "inputSchema": object([]string{"story_id"}, map[string]any{"story_id": uuid})},
		map[string]any{"name": InvocationGetToolName, "title": "Inspect invocation", "description": "Read one single-use work invocation and terminal evidence state.", "inputSchema": object([]string{"invocation_id"}, map[string]any{"invocation_id": uuid})},
		map[string]any{
			"name": EventWaitToolName, "title": "Wait for an organizational event",
			"description": "Block without model polling until a matching event is committed for one exact aggregate, or the bounded timeout expires.",
			"inputSchema": object([]string{"aggregate", "after_revision", "timeout_millis"}, map[string]any{
				"aggregate": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"kind", "id"},
					"properties": map[string]any{
						"kind": map[string]any{"type": "string", "enum": []string{"story", "task", "completion-review", "escalation", "release-plan", "variant-group", "human-participant", "human-interaction", "evidence", "execution", "system", "work-budget-account", "work-invocation"}},
						"id":   uuid,
					},
				},
				"after_revision": map[string]any{"type": "integer", "minimum": 0},
				"event_types":    map[string]any{"type": "array", "maxItems": 64, "uniqueItems": true, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}},
				"timeout_millis": map[string]any{"type": "integer", "minimum": 1, "maximum": 86400000},
			}),
		},
		map[string]any{"name": LibrariesListToolName, "title": "List role libraries", "description": "Inspect exact versions and content identities of configured role libraries.", "inputSchema": object(nil, map[string]any{})},
		map[string]any{"name": LibrariesSyncToolName, "title": "Synchronize role libraries", "description": "Revalidate and synchronize only the exact signed library sources bound by tekrood configuration.", "inputSchema": object(nil, map[string]any{})},
		map[string]any{"name": DiagnosticsToolName, "title": "Inspect operational diagnostics", "description": "Read active tasks, message claims, dead letters, projection faults, role state, and library identities without mutation.", "inputSchema": object(nil, map[string]any{})},
		map[string]any{"name": DeadLetterRepairName, "title": "Repair dead letter", "description": "Append an explicitly authorized DAG successor without reopening the failed delivery or resetting its finite budget.", "inputSchema": object([]string{"failed_message_id", "successor"}, map[string]any{"failed_message_id": uuid, "successor": map[string]any{"type": "object"}})},
		map[string]any{"name": InvocationCancelName, "title": "Cancel invocation", "description": "Request durable cancellation of one exact active invocation with bound evidence.", "inputSchema": object([]string{"invocation_id", "request"}, map[string]any{"invocation_id": uuid, "request": map[string]any{"type": "object"}})},
		map[string]any{"name": FederationInspectName, "title": "Inspect federation", "description": "Inspect exact configured aliases, routes, and public trust grants.", "inputSchema": object(nil, map[string]any{})},
		map[string]any{"name": FederationResolveName, "title": "Resolve federation alias", "description": "Resolve one revisioned convenience alias to its exact actor, deployment, and route.", "inputSchema": object([]string{"alias"}, map[string]any{"alias": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}})},
		map[string]any{"name": FederationSendName, "title": "Send federated message", "description": "Sign and send one exact organizational message through one configured alias and route.", "inputSchema": object([]string{"alias", "message"}, map[string]any{"alias": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}, "message": map[string]any{"type": "object"}})},
		map[string]any{"name": RolesListToolName, "title": "List configured team roles", "description": "Inspect exact role identities and lifecycle state.", "inputSchema": object(nil, map[string]any{})},
		map[string]any{
			"name": RoleControlToolName, "title": "Control one role",
			"description": "Start, stop, restart, pause, or resume one exact role actor.",
			"inputSchema": object([]string{"actor_fqn", "operation"}, map[string]any{
				"actor_fqn": actor,
				"operation": map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "pause", "resume"}},
			}),
		},
		map[string]any{"name": MessageSendToolName, "title": "Send directed team message", "description": "Send one fully bound message between exact active actors.", "inputSchema": map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}},
		map[string]any{"name": MessageGetToolName, "title": "Inspect directed message", "description": "Read one message and its delivery/claim state.", "inputSchema": object([]string{"message_id"}, map[string]any{"message_id": uuid})},
		map[string]any{"name": MessageTraceToolName, "title": "Trace message thread", "description": "Inspect a directed message thread in causal order.", "inputSchema": object([]string{"thread_id"}, map[string]any{"thread_id": uuid})},
		map[string]any{"name": DeadLettersToolName, "title": "List dead letters", "description": "Inspect messages that exhausted bounded delivery.", "inputSchema": object(nil, map[string]any{"recipient": actor})},
	}
}

func hasJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

func accepts(header, target string) bool {
	for _, value := range strings.Split(header, ",") {
		mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err == nil && mediaType == target {
			return true
		}
	}
	return false
}

func secureHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}
