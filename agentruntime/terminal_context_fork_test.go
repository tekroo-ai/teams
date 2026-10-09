package agentruntime

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTerminalRetryContextPreservesRecordedWorkWithoutReplayingTools(t *testing.T) {
	for _, terminal := range []Kind{Failed, Cancelled, TimedOut} {
		t.Run(string(terminal), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			j, _ := forkParent(t, false)
			// Terminate after the original read receipt. No new model request is
			// needed to reconstruct the exact context retained before termination.
			entries := j.streams["parent"][:3]
			payload := json.RawMessage(`"terminated during model request"`)
			if terminal == Failed {
				payload = json.RawMessage(`{"reason":"transport unavailable","class":"TRANSIENT_MODEL_TRANSPORT","retryable":true}`)
			}
			entries = append(entries, Entry{InvocationID: "parent", Sequence: 4, Kind: terminal, Payload: payload})
			j.streams["parent"] = entries
			before, _ := json.Marshal(entries)
			if _, err := CaptureForkSource(context.Background(), j, "parent", testRequestDigest); err == nil {
				t.Fatal("consultation accepted failed source")
			}
			ref, err := CaptureRetryForkSource(context.Background(), j, "parent", testRequestDigest, terminal)
			if err != nil {
				t.Fatal(err)
			}
			fork, err := PrepareContextFork(context.Background(), j, "child", ref)
			if err != nil {
				t.Fatal(err)
			}
			model := &scriptedModel{responses: []Completion{{Text: "Continue the original decision after remediation"}}}
			tool := &countingTool{}
			runner := Runner{Journal: j, Model: model, Tools: tool, ContextFork: fork, SystemPrompt: "Current authority"}
			if _, err := runner.Run(ctx, "child", testRequestDigest, "Apply only the approved correction"); err != nil {
				t.Fatal(err)
			}
			seen, _ := json.Marshal(model.seen)
			if tool.calls != 0 || !strings.Contains(string(seen), "choice A avoids a new dependency") || !strings.Contains(string(seen), "hello") || !strings.Contains(string(seen), "not a successful result") {
				t.Fatalf("lost/misrepresented history: %s; calls=%d", seen, tool.calls)
			}
			after, _ := json.Marshal(j.streams["parent"])
			if !reflect.DeepEqual(before, after) {
				t.Fatal("original journal changed")
			}
		})
	}
}

func TestTerminalRetryContextDoesNotCertifyUnobservedHistoricalCalls(t *testing.T) {
	j, _ := forkParent(t, false)
	entries := j.streams["parent"][:2] // call exists, but no result was recorded
	entries = append(entries, Entry{InvocationID: "parent", Sequence: 3, Kind: Failed, Payload: json.RawMessage(`"failure before observation"`)})
	j.streams["parent"] = entries
	ref, err := CaptureRetryForkSource(context.Background(), j, "parent", testRequestDigest, Failed)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := PrepareContextFork(context.Background(), j, "child", ref)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(fork.WorkingHistory)
	if !strings.Contains(string(encoded), `result_recorded`) || strings.Contains(string(encoded), `\"accepted\":true`) || !validWorkingHistory(fork.WorkingHistory) {
		t.Fatalf("unknown result certified or tool left pending: %s", encoded)
	}
	ref.TerminalKind = TimedOut
	if _, err := LoadForkEntries(context.Background(), j, ref); err == nil {
		t.Fatal("terminal kind tampering accepted")
	}
}
