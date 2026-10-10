package verification

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestVerificationSlotWaitIsCancellableAndReleased(t *testing.T) {
	release, err := Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release()
	release, err = Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if _, err := Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBoundedGoCommandPreservesTestSelectionAndArguments(t *testing.T) {
	in := []string{"/opt/bin/go", "test", "-p", "8", "-parallel=20", "-tags=fixtures", "-timeout=10m", "./...", "-args", "-p=99"}
	want := []string{"/opt/bin/go", "test", "-p=2", "-parallel=2", "-tags=fixtures", "-timeout=10m", "./...", "-args", "-p=99"}
	got, bounded := GoCommand(in)
	if !bounded || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	got, _ = GoCommand(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("not idempotent: %v", got)
	}
	if got, bounded := GoCommand([]string{"git", "show", "HEAD"}); bounded || got[0] != "git" {
		t.Fatal(got)
	}
	if got := Environment([]string{"GOMAXPROCS=99", "GOFLAGS=-tags=fixture"}); !reflect.DeepEqual(got, []string{"GOFLAGS=-tags=fixture", "GOMAXPROCS=2"}) {
		t.Fatal(got)
	}
	got, _ = GoCommand([]string{"go", "test", "-run", "-p=9", "-ldflags", "-parallel=9", "-parallel=1", "./..."})
	if !reflect.DeepEqual(got, []string{"go", "test", "-p=2", "-parallel=1", "-run", "-p=9", "-ldflags", "-parallel=9", "./..."}) {
		t.Fatal("changed non-resource arguments", got)
	}
	got, _ = GoCommand([]string{"go", "test", "-p=1", "./..."})
	if !reflect.DeepEqual(got, []string{"go", "test", "-p=1", "-parallel=2", "./..."}) {
		t.Fatal("increased an explicit lower bound", got)
	}
}

func TestGoTimeoutDetectorUsesExactStreamingReceipt(t *testing.T) {
	for _, input := range []string{"panic: test timed out after 10m0s\n", "panic: test timed out after 20ms", `{"Action":"output","Output":"panic: test timed out after 10m0s\n"}` + "\n"} {
		var d TimeoutDetector
		for _, b := range []byte(input) {
			_, _ = d.Write([]byte{b})
		}
		if !d.TimedOut() {
			t.Fatalf("missed %q", input)
		}
	}
	for _, input := range []string{"assertion: context deadline exceeded\n", "    panic: test timed out after 10m0s\n", "panic: test timed out after invalid\n"} {
		var d TimeoutDetector
		_, _ = d.Write([]byte(input))
		if d.TimedOut() {
			t.Fatalf("false timeout %q", input)
		}
	}
}
