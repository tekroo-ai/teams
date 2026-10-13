//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
)

// A finite scripted prefix exercises real read/paging gateways and watermark
// eviction. Only the successor is a live signed-role model result. No historical
// run, production DB, profile, finite allowance or tool definition is changed.
type freshDeliverySeed struct{ turn int }

func (s *freshDeliverySeed) PrepareTurn(context.Context, []agentruntime.Message) (*agentruntime.PreparedTurn, error) {
	return &agentruntime.PreparedTurn{}, nil
}
func (s *freshDeliverySeed) CompletePrepared(ctx context.Context, h []agentruntime.Message, _ *agentruntime.PreparedTurn) (agentruntime.Completion, error) {
	return s.Complete(ctx, h)
}
func (s *freshDeliverySeed) Complete(_ context.Context, h []agentruntime.Message) (agentruntime.Completion, error) {
	s.turn++
	call := agentruntime.ToolCall{ID: fmt.Sprintf("fresh-seed-%d", s.turn), Name: "read_file"}
	switch {
	case s.turn == 1:
		call.Arguments = json.RawMessage(`{"path":"source.txt"}`)
	case s.turn == 2 || s.turn == 19:
		var ref *agentruntime.RecordedResultRef
		for _, m := range h {
			if m.CallID == "fresh-seed-1" {
				ref = m.Receipt
			}
		}
		if ref == nil {
			return agentruntime.Completion{}, errors.New("seed source reference absent")
		}
		call.Name = "read_recorded_result"
		call.Arguments, _ = json.Marshal(map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "json_pointer": "/output", "byte_offset": 0, "byte_limit": 4096})
	case s.turn >= 3 && s.turn <= 18:
		call.Arguments = json.RawMessage(fmt.Sprintf(`{"path":"pressure-%d.txt"}`, s.turn))
	default:
		return agentruntime.Completion{}, errors.New("finite seed exhausted")
	}
	return agentruntime.Completion{Text: "Retained original decision: preserve-evidence. These synthetic inspections are not executed validation tests.", ToolCalls: []agentruntime.ToolCall{call}}, nil
}

type freshDeliveryCapture struct {
	base    *nativeHTTPCapture
	want    string
	checked bool
}

func (c *freshDeliveryCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	if !c.checked && strings.HasSuffix(req.URL.Path, "/chat/completions") {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var wire struct{ Messages []struct{ Content string } }
		if json.Unmarshal(raw, &wire) != nil || len(wire.Messages) == 0 {
			return nil, errors.New("fresh exact page absent from actual live provider request")
		}
		var observation struct {
			Result struct {
				CallID string          `json:"call_id"`
				Name   string          `json:"name"`
				Output json.RawMessage `json:"output"`
			} `json:"tool_result"`
		}
		if json.Unmarshal([]byte(wire.Messages[len(wire.Messages)-1].Content), &observation) != nil || observation.Result.CallID != "fresh-seed-19" || observation.Result.Name != "read_recorded_result" || string(observation.Result.Output) != c.want {
			return nil, errors.New("fresh page bytes or call identity changed in actual structured provider request")
		}
		c.checked = true
	}
	return c.base.RoundTrip(req)
}

func TestQualifyNativeFreshDeliveryAllSignedRoles(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Minute)
	defer cancel()
	config, team, profiles, output := terminalQualificationEnvironment(t, ctx)
	if len(profiles) != 7 {
		t.Fatal("expected all seven current qualified signed roles")
	}
	for _, p := range profiles {
		t.Run(string(p.RoleFQRN), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
			defer cancel()
			root := filepath.Join(output, string(p.RoleFQRN)+"-fixture")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			marker := string(digestBytes([]byte(root + " original exact page marker")))
			for name, text := range map[string]string{
				"AGENTS.md":  "Synthetic read-only delivery qualification, not feature work. Retain original context. No edits, tests, retries or production operations.\n",
				"source.txt": "source_marker=" + marker + "\n" + strings.Repeat("retained original source ", 800),
			} {
				if err := captureWrite(filepath.Join(root, name), []byte(text)); err != nil {
					t.Fatal(err)
				}
			}
			for i := 3; i <= 18; i++ {
				text := fmt.Sprintf("distinct pressure source %d\n", i) + strings.Repeat("exact evidence ", 400)
				if err := captureWrite(filepath.Join(root, fmt.Sprintf("pressure-%d.txt", i)), []byte(text)); err != nil {
					t.Fatal(err)
				}
			}
			id := deterministicOperationalUUID("fresh-delivery-qualification", root)
			brief := terminalQualificationBrief(t, team, p, id, root, "Synthetic fresh-evidence delivery qualification, not feature work. Continue the retained original decision. The latest read_recorded_result response is a newly requested first source page. Return decision=preserve-evidence and source_marker equal to the 64 hex characters after source_marker= in that exact page. The marker is not supplied in this assignment or schema. No further repository discovery, edits or test execution is needed. Never infer named validation PASS from these reads.")
			j := &terminalQualificationJournal{streams: map[string][]agentruntime.Entry{}, directory: output}
			capture := &freshDeliveryCapture{base: &nativeHTTPCapture{directory: output, scenario: string(p.RoleFQRN)}}
			cfg := terminalQualificationConfig(team, p, root, j, &http.Client{Timeout: 10 * time.Minute, Transport: capture}, config.Native.ContextDelivery)
			schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["decision","source_marker"],"properties":{"decision":{"type":"string"},"source_marker":{"type":"string","minLength":64,"maxLength":64}}}`)
			cfg.ResultContract = terminalQualificationContract(brief, schema)
			session := terminalQualificationSession(t, ctx, brief, cfg)
			// Zero is the existing deadline-bounded profile setting, not a
			// nineteen-turn limit. Never enlarge a nonzero admitted turn cap.
			if p.MaxTurns > 0 && p.MaxTurns <= 19 {
				t.Fatal("fixture does not fit unchanged finite allowance")
			}
			seed := session.Runner
			seed.Model = &freshDeliverySeed{}
			seed.MaxTurns = 19
			if _, err := seed.Run(ctx, session.InvocationID, session.RequestDigest, session.Prompt); !errors.Is(err, agentruntime.ErrTurnLimit) {
				t.Fatal("finite seed", err)
			}
			prefixCount := len(j.streams[session.InvocationID])
			before, _ := json.Marshal(j.streams[session.InvocationID])
			var oldPage, freshPage agentruntime.ToolResult
			oldPageHidden := false
			for _, e := range j.streams[session.InvocationID] {
				if e.Kind == agentruntime.ToolDone {
					var d agentruntime.ToolResult
					json.Unmarshal(e.Payload, &d)
					if d.Error != "" {
						t.Fatal("seed tool error", d.Error)
					}
					if d.CallID == "fresh-seed-2" {
						oldPage = d
					}
					if d.CallID == "fresh-seed-19" {
						freshPage = d
					}
				}
				if e.Kind == agentruntime.Compacted {
					var cp struct {
						Delta struct {
							Replacements []struct {
								Index   int
								Content string
							}
						} `json:"delta"`
					}
					if json.Unmarshal(e.Payload, &cp) != nil {
						t.Fatal("invalid pressure checkpoint")
					}
					// The finite prefix has two messages per tool turn after the
					// pinned layers: turn 2's page is working-history index 3.
					for _, replacement := range cp.Delta.Replacements {
						if replacement.Index == 3 && strings.Contains(replacement.Content, "RECORDED_RESULT_HANDLE") {
							oldPageHidden = true
						}
					}
				}
			}
			// The unit replay independently tests the exact old-copy hiding. This
			// live gate additionally requires actual pressure and identical pages.
			if !oldPageHidden || len(freshPage.Output) == 0 || !bytes.Equal(oldPage.Output, freshPage.Output) {
				t.Fatal("fixture missed duplicate exact-page pressure")
			}
			capture.want = string(freshPage.Output)
			result, err := session.Run(ctx)
			if err != nil {
				t.Fatal("live delivery", err)
			}
			var value struct {
				Decision string
				Marker   string `json:"source_marker"`
			}
			if json.Unmarshal(bytes.TrimPrefix([]byte(result), []byte(application.OrganizationalResultMarker+"\n")), &value) != nil || value.Decision != "preserve-evidence" || value.Marker != marker || !capture.checked {
				t.Fatal("live result did not preserve original decision/exact marker", result)
			}
			after, _ := json.Marshal(j.streams[session.InvocationID][:prefixCount])
			if !bytes.Equal(before, after) {
				t.Fatal("prior context or evidence rewritten")
			}
			session.Runner.Model = &terminalQualificationModel{}
			if _, err := session.Run(ctx); err != nil {
				t.Fatal("completed restart", err)
			}
			terminalQualificationWrite(t, output, string(p.RoleFQRN)+"-result.json", map[string]any{"status": "PASS", "scope": "synthetic fresh page pressure and live signed-role delivery, not feature completion", "profile": p, "invocation_id": id, "scripted_prefix_turns": 19, "exact_live_request_checked": capture.checked, "raw_prefix_preserved": true, "invocation_retries": 0, "result": result})
		})
		if t.Failed() {
			break
		}
	}
}
