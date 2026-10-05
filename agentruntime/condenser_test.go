package agentruntime

import (
	"context"
	"testing"
)

type discardHistory struct{}

func (discardHistory) Condense(_ context.Context, history []Message) ([]Message, error) {
	if len(history) > 0 {
		history[0].Content = "modified working history"
	}
	return []Message{{Role: "user", Content: "checkpoint"}}, nil
}

func TestCondenserCannotRemovePinnedInstructionsOrTask(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "all five original instruction layers"},
		{Role: "user", Content: "original admitted task brief"},
		{Role: "assistant", Content: "old working turn"},
	}
	condensed, err := condenseHistory(context.Background(), messages, discardHistory{})
	if err != nil || len(condensed) != 3 || condensed[0].Content != messages[0].Content || condensed[1].Content != messages[1].Content || condensed[2].Content != "checkpoint" || messages[2].Content != "old working turn" {
		t.Fatalf("pinned inputs were altered: messages=%+v condensed=%+v err=%v", messages, condensed, err)
	}
}
