package mongo

import (
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestExtendDeadlineAcrossSuspensionsCountsOnlyInvocationLifetime(t *testing.T) {
	deployment := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	authorizedAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	deadline := authorizedAt.Add(time.Hour)
	window := func(start, end time.Time) RuntimeSuspensionWindow {
		return RuntimeSuspensionWindow{ID: runtimeSuspensionID(deployment, start, end), DeploymentIdentity: deployment, SuspendedAt: start, ResumedAt: end}
	}
	windows := []RuntimeSuspensionWindow{
		window(authorizedAt.Add(-time.Hour), authorizedAt.Add(-30*time.Minute)),
		window(authorizedAt.Add(20*time.Minute), authorizedAt.Add(40*time.Minute)),
		// The first suspension moves the deadline to 11:20, so this second
		// suspension is still inside the invocation's effective lifetime.
		window(authorizedAt.Add(70*time.Minute), authorizedAt.Add(80*time.Minute)),
		window(authorizedAt.Add(2*time.Hour), authorizedAt.Add(3*time.Hour)),
	}
	got, err := extendDeadlineAcrossSuspensions(deadline, authorizedAt, windows)
	if err != nil {
		t.Fatal(err)
	}
	want := deadline.Add(30 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("effective deadline = %s, want %s", got, want)
	}
}

func TestRuntimeSuspensionIdentityRejectsMutation(t *testing.T) {
	deployment := kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	window := RuntimeSuspensionWindow{ID: runtimeSuspensionID(deployment, start, start.Add(time.Minute)), DeploymentIdentity: deployment, SuspendedAt: start, ResumedAt: start.Add(time.Minute)}
	if !window.Valid() {
		t.Fatal("valid content-addressed suspension was rejected")
	}
	window.ResumedAt = window.ResumedAt.Add(time.Second)
	if window.Valid() {
		t.Fatal("mutated suspension retained its prior identity")
	}
}

func TestPendingRuntimeSuspensionClosesWakeRaceBeforeHeartbeatPersists(t *testing.T) {
	deployment := kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	lastHeartbeat := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	session := runtimeSessionDocument{
		ID: string(deployment), Revision: 3, SessionID: "00000000-0000-7000-8000-000000000001",
		State: runtimeSessionRunning, LastHeartbeatAt: lastHeartbeat, SuspensionThresholdMillis: 10_000,
	}
	if _, observed, err := pendingRuntimeSuspension(deployment, session, lastHeartbeat.Add(10*time.Second)); err != nil || observed {
		t.Fatalf("threshold boundary observed=%v err=%v", observed, err)
	}
	window, observed, err := pendingRuntimeSuspension(deployment, session, lastHeartbeat.Add(2*time.Hour))
	if err != nil || !observed {
		t.Fatalf("wake gap observed=%v err=%v", observed, err)
	}
	if got, want := window.Duration(), 2*time.Hour; got != want {
		t.Fatalf("wake gap duration=%s want=%s", got, want)
	}
	if runtimeSuspensionCovered(nil, window) {
		t.Fatal("uncommitted wake gap was reported as covered")
	}
	committed := window
	committed.ResumedAt = committed.ResumedAt.Add(time.Second)
	committed.ID = runtimeSuspensionID(deployment, committed.SuspendedAt, committed.ResumedAt)
	if !runtimeSuspensionCovered([]RuntimeSuspensionWindow{committed}, window) {
		t.Fatal("heartbeat window committed during the read race did not cover the pending gap")
	}
}
