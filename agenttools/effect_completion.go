package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

// Persist outcomes independently of the execution deadline: a finished action
// must not lose its receipt merely because cancellation/deadline just fired.
func completeEffect(ledger EffectLedger, request Request, intent EffectRecord, result Result) (Receipt, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ledger.Complete(ctx, intent, encoded); err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	return effectReceipt(request, intent.ArgumentsSHA256, result)
}

func failedEffectResult(name string, result Result, err error) Result {
	result.Name = name
	result.ExitCode = -1
	result.ExecutionError = err.Error()
	if len(result.ExecutionError) > 4096 {
		end := 4096
		for !utf8.RuneStart(result.ExecutionError[end]) {
			end--
		}
		result.ExecutionError = result.ExecutionError[:end]
	}
	sum := sha256.Sum256([]byte(result.Output))
	result.SHA256 = hex.EncodeToString(sum[:])
	return result
}

func validFailedEffectResult(name string, result Result) bool {
	if result.Name != name || result.ExitCode != -1 || result.ExecutionError == "" || len(result.ExecutionError) > 4096 || !utf8.ValidString(result.ExecutionError) {
		return false
	}
	sum := sha256.Sum256([]byte(result.Output))
	return result.SHA256 == hex.EncodeToString(sum[:])
}

// Used only before the operation's externally visible mutation. Errors after
// a possible mutation require state inspection, never this classification.
type effectNotAppliedError struct{ error }

func (err effectNotAppliedError) Unwrap() error { return err.error }

func notApplied(err error) error { return effectNotAppliedError{err} }
