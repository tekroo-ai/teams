package operationalruntime

import (
	"context"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestUUIDv7SourceProducesValidUniqueTimeOrderedFormat(t *testing.T) {
	source, err := NewUUIDv7Source(fixedClock{now: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]struct{}, 1000)
	for index := 0; index < 1000; index++ {
		identity, err := source.Next()
		if err != nil || !identity.Valid() || identity[14] != '7' || !variant(identity[19]) {
			t.Fatalf("identity=%q err=%v", identity, err)
		}
		if _, duplicate := seen[string(identity)]; duplicate {
			t.Fatalf("duplicate identity %s", identity)
		}
		seen[string(identity)] = struct{}{}
	}
}

func TestRuntimeRejectsIncompleteAssemblyWithoutOpeningResources(t *testing.T) {
	if _, err := New(context.Background(), Config{}); err != application.ErrInvalidConfiguration {
		t.Fatalf("error = %v", err)
	}
}

func variant(value byte) bool {
	return value == '8' || value == '9' || value == 'a' || value == 'b'
}
