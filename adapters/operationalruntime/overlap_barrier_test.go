//go:build mongo_integration

package operationalruntime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestIntegratedOverlapBarrierRequiresTwoDurableStarts(t *testing.T) {
	for _, name := range []string{"one worker", "authorized only", "nil start", "duplicate start", "terminal worker", "expired", "both started"} {
		t.Run(name, func(t *testing.T) {
			first, second := deterministicOperationalUUID("barrier", "first"), deterministicOperationalUUID("barrier", "second")
			server := &integratedOpenHands{t: t, conversations: map[string]*integratedConversation{}, active: 2}
			for _, id := range []kernel.UUIDv7{first, second} {
				raw, _ := json.Marshal(application.ExecutionBrief{InvocationID: id})
				server.conversations[string(id)] = &integratedConversation{submitted: true, prompt: string(raw), readyAt: time.Now().Add(-time.Hour)}
			}
			barrier := server.installCompletionBarrier(time.Now().Add(time.Minute), string(first), string(second))
			now := time.Now().UTC()
			started := []kernel.WorkInvocation{{ID: first, State: kernel.InvocationStarted, StartedAt: &now}, {ID: second, State: kernel.InvocationStarted, StartedAt: &now}}
			switch name {
			case "one worker":
				started = started[:1]
			case "authorized only":
				started[1].State = kernel.InvocationAuthorized
			case "nil start":
				started[1].StartedAt = nil
			case "duplicate start":
				started[1] = started[0]
			case "terminal worker":
				started[0].State = kernel.InvocationSucceeded
			case "expired":
				barrier.deadline = time.Now().Add(-time.Second)
			}
			// A single worker stays active even if arbitrary admission latency
			// has exceeded the old 750 ms delay. No sleep/timing race is required.
			if name != "expired" {
				server.refresh(server.conversations[string(first)])
				if server.conversations[string(first)].finished {
					t.Fatal("worker completed before barrier release")
				}
			}
			err := server.releaseCompletionBarrier(barrier, started...)
			if name == "both started" {
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range []kernel.UUIDv7{first, second} {
					server.refresh(server.conversations[string(id)])
					if !server.conversations[string(id)].finished {
						t.Fatal("released worker did not finish")
					}
				}
				if server.active != 0 {
					t.Fatal("fixture leaked active workers")
				}
			} else {
				if err == nil || barrier.released {
					t.Fatal("missing/invalid worker start accepted")
				}
				if name == "expired" {
					server.refresh(server.conversations[string(first)])
					if !barrier.expired || !server.conversations[string(first)].interrupted || server.conversations[string(first)].finished {
						t.Fatal("expired barrier did not fail closed")
					}
				}
			}
		})
	}
}
