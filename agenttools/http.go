package agenttools

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tekroo-ai/teams/kernel"
)

const ReadOnlyMCPPath = "/agent-tools/"

// SessionEndpoint carries only one invocation's read-only MCP credential.
// Its bearer token is derived from a persistent server secret, so an active
// invocation can reconnect after a daemon restart without session storage.
type SessionEndpoint struct {
	URL         string
	BearerToken string
}

type HTTPService struct {
	gateway Gateway
	baseURL string
	key     []byte
}

func NewHTTPService(gateway Gateway, baseURL string, key []byte) (*HTTPService, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" || len(key) < 32 || gateway.Bindings == nil || gateway.Host.Timeout <= 0 {
		return nil, ErrInvalidCall
	}
	host, port, err := net.SplitHostPort(endpoint.Host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return nil, ErrInvalidCall
	}
	return &HTTPService{gateway: gateway, baseURL: baseURL, key: append([]byte(nil), key...)}, nil
}

func (service *HTTPService) IssueReadOnlySession(ctx context.Context, invocationID kernel.UUIDv7, requestDigest kernel.Digest) (SessionEndpoint, error) {
	if service == nil || !invocationID.Valid() || !requestDigest.Valid() {
		return SessionEndpoint{}, ErrInvalidCall
	}
	operation, cancel := context.WithTimeout(ctx, service.gateway.Host.Timeout)
	defer cancel()
	if _, err := service.gateway.Bindings.BindToolInvocation(operation, invocationID, requestDigest); err != nil {
		return SessionEndpoint{}, err
	}
	return SessionEndpoint{
		URL:         service.baseURL + ReadOnlyMCPPath + string(invocationID) + "/" + string(requestDigest),
		BearerToken: service.token(invocationID, requestDigest),
	}, nil
}

func (service *HTTPService) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if service == nil {
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
			return
		}
		path := strings.TrimPrefix(request.URL.Path, ReadOnlyMCPPath)
		parts := strings.Split(path, "/")
		if !strings.HasPrefix(request.URL.Path, ReadOnlyMCPPath) || len(parts) != 2 || request.URL.RawQuery != "" {
			http.NotFound(writer, request)
			return
		}
		invocationID, requestDigest := kernel.UUIDv7(parts[0]), kernel.Digest(parts[1])
		if !invocationID.Valid() || !requestDigest.Valid() {
			http.NotFound(writer, request)
			return
		}
		want := "Bearer " + service.token(invocationID, requestDigest)
		got := request.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		server, err := NewReadOnlyMCPServer(request.Context(), service.gateway, invocationID, requestDigest)
		if err != nil {
			if errors.Is(err, ErrStaleBinding) || errors.Is(err, ErrForbidden) {
				http.Error(writer, "invocation unavailable", http.StatusForbidden)
			} else {
				http.Error(writer, "tool service unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
		transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
		transport.ServeHTTP(writer, request)
	})
}

func (service *HTTPService) token(invocationID kernel.UUIDv7, requestDigest kernel.Digest) string {
	mac := hmac.New(sha256.New, service.key)
	_, _ = mac.Write([]byte("tekroo-agent-tools-v1\x00"))
	_, _ = mac.Write([]byte(invocationID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(requestDigest))
	return hex.EncodeToString(mac.Sum(nil))
}
