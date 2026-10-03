package nativeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/agentruntime"
)

// SMAContextBridge is an opt-in native equivalent of the accepted
// UserPromptSubmit retrieval hook. It requests semantic context only; Teams
// never treats the returned text as organizational authority.
type SMAContextBridge struct {
	BaseURL string
	Timeout time.Duration
}

func (bridge SMAContextBridge) Retrieve(ctx context.Context, sessionID, workingDir, prompt string) (agentruntime.SemanticContext, error) {
	endpoint, err := url.Parse(bridge.BaseURL)
	if err != nil || endpoint.Scheme != "http" || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() || endpoint.User != nil || endpoint.Path != "" && endpoint.Path != "/" || endpoint.RawQuery != "" || endpoint.Fragment != "" || bridge.Timeout <= 0 || bridge.Timeout > time.Second || sessionID == "" || workingDir == "" || prompt == "" {
		return agentruntime.SemanticContext{}, agentruntime.ErrInvalidContextConfiguration
	}
	requestBody, _ := json.Marshal(struct {
		SessionID  string `json:"session_id"`
		WorkingDir string `json:"working_dir"`
		Prompt     string `json:"prompt"`
	}{sessionID, workingDir, prompt})
	operation, cancel := context.WithTimeout(ctx, bridge.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(operation, http.MethodPost, strings.TrimRight(bridge.BaseURL, "/")+"/v1/openhands/context", bytes.NewReader(requestBody))
	if err != nil {
		return agentruntime.SemanticContext{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return agentruntime.SemanticContext{}, ctx.Err()
		}
		return agentruntime.SemanticContext{Status: "UNAVAILABLE"}, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return agentruntime.SemanticContext{Status: "UNAVAILABLE"}, nil
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 8<<10+1))
	if err != nil || len(content) > 8<<10 {
		return agentruntime.SemanticContext{Status: "INVALID_RESPONSE"}, nil
	}
	var decoded struct {
		HitCount     json.Number `json:"hit_count"`
		ContextBlock string      `json:"context_block"`
		TraceID      string      `json:"trace_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF {
		return agentruntime.SemanticContext{Status: "INVALID_RESPONSE"}, nil
	}
	result := agentruntime.SemanticContext{Status: "EMPTY"}
	if strings.HasPrefix(decoded.TraceID, "ctx_") && len(decoded.TraceID) <= 64 {
		result.TraceID = decoded.TraceID
	}
	hits, err := decoded.HitCount.Float64()
	if err != nil || hits <= 0 {
		return result, nil
	}
	if strings.TrimSpace(decoded.ContextBlock) == "" || len(decoded.ContextBlock) > 4096 || !utf8.ValidString(decoded.ContextBlock) {
		return agentruntime.SemanticContext{Status: "INVALID_RESPONSE", TraceID: result.TraceID}, nil
	}
	result.Status = "AVAILABLE"
	result.Block = decoded.ContextBlock
	return result, nil
}
