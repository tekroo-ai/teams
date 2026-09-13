package operationalruntime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/kernel"
)

func TestBoundedWorkBlockReasonPreservesShortReason(t *testing.T) {
	reason := "validation did not pass"
	if got := boundedWorkBlockReason(reason, kernel.UUIDv7("00000000-0000-7000-8000-000000000001")); got != reason {
		t.Fatalf("reason=%q, want %q", got, reason)
	}
}

func TestBoundedWorkBlockReasonRetainsEvidencePointerWithinContractLimit(t *testing.T) {
	invocationID := kernel.UUIDv7("00000000-0000-7000-8000-000000000001")
	reason := strings.Repeat("界", maximumWorkBlockReasonRunes+100)
	got := boundedWorkBlockReason(reason, invocationID)
	if count := utf8.RuneCountInString(got); count != maximumWorkBlockReasonRunes {
		t.Fatalf("rune count=%d, want %d", count, maximumWorkBlockReasonRunes)
	}
	if !strings.HasPrefix(got, strings.Repeat("界", 100)) {
		t.Fatal("bounded reason did not preserve the beginning of the validator result")
	}
	if !strings.HasSuffix(got, "teams://work-invocation/"+string(invocationID)) {
		t.Fatal("bounded reason did not retain the full-result evidence pointer")
	}
}
