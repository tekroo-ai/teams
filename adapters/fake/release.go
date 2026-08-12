package fake

import (
	"context"
	"errors"
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrReleaseProviderUnscripted = errors.New("release provider call is not scripted")

type ReleaseProviderStep struct {
	Observation         kernel.ReleaseProviderObservation
	Err                 error
	WaitForCancellation bool
}

type cachedReleaseStep struct {
	observation kernel.ReleaseProviderObservation
	err         error
	wait        bool
}

// ReleaseProvider is a deterministic, mutation-free provider adapter. Merge
// responses are cached by the contract idempotency key so duplicate and
// concurrent calls converge on one scripted external outcome.
type ReleaseProvider struct {
	mu              sync.Mutex
	mergeScripts    map[string]ReleaseProviderStep
	mergeResults    map[string]cachedReleaseStep
	reconciliations map[kernel.UUIDv7]ReleaseProviderStep
	mergeCalls      map[string]uint64
	mergeEffects    map[string]uint64
	reconcileCalls  map[kernel.UUIDv7]uint64
}

func NewReleaseProvider() *ReleaseProvider {
	return &ReleaseProvider{
		mergeScripts: make(map[string]ReleaseProviderStep), mergeResults: make(map[string]cachedReleaseStep),
		reconciliations: make(map[kernel.UUIDv7]ReleaseProviderStep), mergeCalls: make(map[string]uint64), mergeEffects: make(map[string]uint64), reconcileCalls: make(map[kernel.UUIDv7]uint64),
	}
}

func (provider *ReleaseProvider) ScriptMerge(key string, step ReleaseProviderStep) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.mergeScripts[key] = step
}

func (provider *ReleaseProvider) ScriptReconciliation(attemptID kernel.UUIDv7, step ReleaseProviderStep) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.reconciliations[attemptID] = step
}

func (provider *ReleaseProvider) Merge(ctx context.Context, request kernel.ReleaseMergeRequest) (kernel.ReleaseProviderObservation, error) {
	provider.mu.Lock()
	provider.mergeCalls[request.ProviderIdempotencyKey]++
	result, found := provider.mergeResults[request.ProviderIdempotencyKey]
	if !found {
		step, scripted := provider.mergeScripts[request.ProviderIdempotencyKey]
		if !scripted {
			provider.mu.Unlock()
			return kernel.ReleaseProviderObservation{}, ErrReleaseProviderUnscripted
		}
		result = cachedReleaseStep{observation: step.Observation, err: step.Err, wait: step.WaitForCancellation}
		provider.mergeResults[request.ProviderIdempotencyKey] = result
		provider.mergeEffects[request.ProviderIdempotencyKey]++
	}
	provider.mu.Unlock()
	if result.wait {
		<-ctx.Done()
		return kernel.ReleaseProviderObservation{}, ctx.Err()
	}
	return result.observation, result.err
}

func (provider *ReleaseProvider) MergeEffects(key string) uint64 {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.mergeEffects[key]
}

func (provider *ReleaseProvider) Reconcile(ctx context.Context, request kernel.ReleaseMergeRequest) (kernel.ReleaseProviderObservation, error) {
	provider.mu.Lock()
	provider.reconcileCalls[request.AttemptID]++
	step, found := provider.reconciliations[request.AttemptID]
	provider.mu.Unlock()
	if !found {
		return kernel.ReleaseProviderObservation{}, ErrReleaseProviderUnscripted
	}
	if step.WaitForCancellation {
		<-ctx.Done()
		return kernel.ReleaseProviderObservation{}, ctx.Err()
	}
	return step.Observation, step.Err
}

func (provider *ReleaseProvider) MergeCalls(key string) uint64 {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.mergeCalls[key]
}

func (provider *ReleaseProvider) ReconciliationCalls(attemptID kernel.UUIDv7) uint64 {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.reconcileCalls[attemptID]
}
