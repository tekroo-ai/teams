package agentruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// Reasoning exhaustion is not a truncated JSON prefix. Retrying the same
// request cannot continue it; recovery needs an explicit changed condition.
var ErrReasoningBudgetExhausted = errors.New("model output budget exhausted by reasoning before structured output")

type Failure struct {
	Reason    string `json:"reason"`
	Class     string `json:"class"`
	Retryable bool   `json:"retryable"`
}

func ClassifyFailure(err error) Failure {
	if err == nil {
		return Failure{Reason: "missing failure cause", Class: "EXECUTION_FAILURE"}
	}
	f := Failure{Reason: err.Error(), Class: "EXECUTION_FAILURE"}
	if len(f.Reason) > 4096 {
		f.Reason = f.Reason[:4096]
		for !utf8.ValidString(f.Reason) {
			f.Reason = f.Reason[:len(f.Reason)-1]
		}
	}
	switch {
	case errors.Is(err, ErrReasoningBudgetExhausted):
		f.Class = "REASONING_BUDGET_EXHAUSTED"
	case errors.Is(err, ErrInvalidTurn), errors.Is(err, ErrTurnLimit), errors.Is(err, ErrInvalidContextConfiguration):
		f.Class = "INVALID_MODEL_TURN"
	default:
		// Only an explicitly identified transient model transport failure is
		// automatically retryable. Unknown failures are not transient by default.
		f.Retryable = errors.Is(err, ErrTransientModel)
		if f.Retryable {
			f.Class = "TRANSIENT_MODEL_TRANSPORT"
		}
	}
	return f
}

var ErrTransientModel = errors.New("transient model transport failure")

// DecodeFailure accepts legacy string journal entries without inventing a
// retryable classification for them.
func DecodeFailure(raw json.RawMessage) (Failure, error) {
	var reason string
	if json.Unmarshal(raw, &reason) == nil {
		if reason == "" {
			return Failure{}, ErrInvalidTurn
		}
		return Failure{Reason: reason, Class: "LEGACY_UNCLASSIFIED"}, nil
	}
	var f Failure
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return Failure{}, err
	}
	if d.Decode(&struct{}{}) != io.EOF || f.Reason == "" || len(f.Reason) > 4096 {
		return Failure{}, ErrInvalidTurn
	}
	switch f.Class {
	case "EXECUTION_FAILURE", "INVALID_MODEL_TURN", "REASONING_BUDGET_EXHAUSTED":
		if f.Retryable {
			return Failure{}, ErrInvalidTurn
		}
	case "TRANSIENT_MODEL_TRANSPORT":
		if !f.Retryable {
			return Failure{}, ErrInvalidTurn
		}
	default:
		return Failure{}, ErrInvalidTurn
	}
	return f, nil
}
