package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type sleepEffects struct {
	name     string
	held     *bool
	physical int
	result   json.RawMessage
}

func (effects *sleepEffects) Handles(name string) bool { return name == effects.name }
func (effects *sleepEffects) ExecuteEffect(context.Context, ToolCall) (json.RawMessage, error) {
	if effects.result == nil {
		effects.physical++
		effects.result = json.RawMessage(`{"applied":true}`)
		*effects.held = false // Suspension between physical completion and ToolDone.
	}
	return effects.result, nil
}
func (effects *sleepEffects) ReconcileEffect(context.Context, ToolCall) (json.RawMessage, bool, error) {
	return effects.result, effects.result != nil, nil
}

func TestSleepRecoveryDoesNotRepeatRecordedModelOrAppliedEffect(t *testing.T) {
	for _, name := range []string{"write_file", "edit_file", "git_stage_files", "git_commit"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			held := true
			journal := &memoryJournal{}
			model := &scriptedModel{responses: []Completion{
				{ToolCalls: []ToolCall{{ID: "same-call", Name: name, Arguments: json.RawMessage(`{}`)}}},
				{Text: "done"},
			}}
			effects := &sleepEffects{name: name, held: &held}
			runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}, Effects: effects,
				CheckOwnership: func(context.Context) error {
					if !held {
						return ErrLeaseLost
					}
					return nil
				}}
			if _, err := runner.Run(ctx, "same-invocation", testRequestDigest, "same task"); !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("interrupted run = %v", err)
			}
			if len(journal.entries) != 2 || journal.entries[1].Kind != ModelTurn || effects.physical != 1 {
				t.Fatalf("checkpoint = %+v, effects=%d", journal.entries, effects.physical)
			}
			held = true
			// Inspect the reserved call before resuming: inspection itself must
			// not initiate a second write, stage, or commit.
			if err := runner.ReconcilePendingEffects(ctx, "same-invocation", testRequestDigest, "same task"); err != nil {
				t.Fatal(err)
			}
			if result, err := runner.Run(ctx, "same-invocation", testRequestDigest, "same task"); err != nil || result != "done" {
				t.Fatalf("resume = %q, %v", result, err)
			}
			if effects.physical != 1 || len(model.seen) != 2 {
				t.Fatalf("repeated completed work: physical=%d model=%d", effects.physical, len(model.seen))
			}
			if _, err := runner.Run(ctx, "same-invocation", testRequestDigest, "same task"); err != nil || effects.physical != 1 || len(model.seen) != 2 {
				t.Fatalf("terminal replay: %v", err)
			}
		})
	}
}

func TestSleepRecoveryResubmitsRecordedFinalResultWithoutNewModelTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	held := true
	journal := &memoryJournal{}
	model := &scriptedModel{responses: []Completion{{ToolCalls: []ToolCall{{ID: "final", Name: "submit_result", Arguments: json.RawMessage(`{"outcome":"completed"}`)}}}}}
	checks := 0
	runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}, FinalTool: "submit_result",
		Finalize: func(json.RawMessage) (string, error) { return "completed", nil },
		ValidateFinalResult: func(context.Context, []byte) error {
			checks++
			if checks == 1 {
				held = false
			}
			return nil
		},
		CheckOwnership: func(context.Context) error {
			if !held {
				return ErrLeaseLost
			}
			return nil
		}}
	if _, err := runner.Run(ctx, "same-invocation", testRequestDigest, "same task"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("interrupted finalization=%v", err)
	}
	held = true
	if result, err := runner.Run(ctx, "same-invocation", testRequestDigest, "same task"); err != nil || result != "completed" {
		t.Fatalf("resume=%q %v", result, err)
	}
	if len(model.seen) != 1 || checks != 2 || len(journal.entries) != 3 || journal.entries[2].Kind != Finished {
		t.Fatalf("completion replay: model=%d checks=%d journal=%+v", len(model.seen), checks, journal.entries)
	}
}

type sleepErrorModel struct{ held *bool }

func (model sleepErrorModel) Complete(context.Context, []Message) (Completion, error) {
	*model.held = false
	return Completion{}, context.DeadlineExceeded
}

func TestRequestTimeoutOnWakeIsLeaseLossNotTaskFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	held := true
	journal := &memoryJournal{}
	runner := Runner{Journal: journal, Model: sleepErrorModel{held: &held}, Tools: &countingTool{},
		CheckOwnership: func(context.Context) error {
			if !held {
				return ErrLeaseLost
			}
			return nil
		}}
	if _, err := runner.Run(ctx, "same-invocation", testRequestDigest, "same task"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("sleep timeout=%v", err)
	}
	if len(journal.entries) != 1 || journal.entries[0].Kind != Started {
		t.Fatalf("corrupted checkpoint=%+v", journal.entries)
	}
}

func TestAbsoluteDeadlineIsNotExtendedByRecovery(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	journal := &memoryJournal{}
	model := &scriptedModel{}
	runner := Runner{Journal: journal, Model: model, Tools: &countingTool{}}
	if _, err := runner.Run(ctx, "same-invocation", testRequestDigest, "same task"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired absolute deadline=%v", err)
	}
	if len(journal.entries) != 0 || len(model.seen) != 0 {
		t.Fatal("started work beyond absolute deadline")
	}
}
