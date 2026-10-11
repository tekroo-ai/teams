package operationalruntime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/internal/verification"
)

var errCandidatePreflightPending = errors.New("candidate preflight pending")

type candidateAdmissionContextKey struct{}
type candidateGateIdentityContextKey struct{}
type candidateGateIdentity struct{ commit, tree string }
type candidateAdmissionContext struct {
	lifetime context.Context
	wake     chan struct{}
	timeout  time.Duration
}
type candidatePreflightJob struct {
	class    verification.Class
	cancel   context.CancelFunc
	done     chan struct{}
	receipts []candidateGateReceipt
	err      error
}
type candidatePreflights struct {
	sync.Mutex
	jobs map[string]*candidatePreflightJob
	wait sync.WaitGroup
}

// Only admission uses this path. Recovery/qualification retain their synchronous
// API. The service lifetime, not the short reconciliation pass, owns a job.
// Pending work does not consume an invocation or mutate a task/budget state.
func (p *candidatePreflights) admit(ctx context.Context, key string, class verification.Class, binding candidateAdmissionContext, run func(context.Context) ([]candidateGateReceipt, error)) ([]candidateGateReceipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.Lock()
	if err := ctx.Err(); err != nil {
		p.Unlock()
		return nil, err
	}
	if job := p.jobs[key]; job != nil {
		select {
		case <-job.done:
			delete(p.jobs, key)
			p.Unlock()
			return job.receipts, job.err
		default:
			p.Unlock()
			return nil, errCandidatePreflightPending
		}
	}
	// Bound live preparation too, not merely the subprocess queue. Settled
	// jobs must not reserve a lane after their source/frontier is superseded.
	// Their gates already have durable success/failure receipts on disk.
	active, broad, settled := 0, 0, 0
	for _, existing := range p.jobs {
		select {
		case <-existing.done:
			settled++
		default:
			active++
			if existing.class == verification.WholeModule {
				broad++
			}
		}
	}
	if active >= verification.MaxConcurrent {
		p.Unlock()
		return nil, errCandidatePreflightPending
	}
	if class == verification.WholeModule && broad > 0 {
		p.Unlock()
		return nil, errCandidatePreflightPending
	}
	if settled >= 64 {
		for key, existing := range p.jobs {
			select {
			case <-existing.done:
				delete(p.jobs, key)
				settled--
			default:
			}
			if settled < 64 {
				break
			}
		}
	}
	if err := binding.lifetime.Err(); err != nil {
		p.Unlock()
		return nil, err
	}
	jobCtx, cancel := context.WithTimeout(binding.lifetime, binding.timeout)
	job := &candidatePreflightJob{class: class, cancel: cancel, done: make(chan struct{})}
	if p.jobs == nil {
		p.jobs = map[string]*candidatePreflightJob{}
	}
	p.jobs[key] = job
	p.wait.Add(1)
	p.Unlock()
	go func() {
		defer p.wait.Done()
		defer cancel()
		job.receipts, job.err = run(jobCtx)
		close(job.done)
		select {
		case binding.wake <- struct{}{}:
		default:
		}
	}()
	return nil, errCandidatePreflightPending
}

func (p *candidatePreflights) cancelAll() {
	if p == nil {
		return
	}
	p.Lock()
	defer p.Unlock()
	for key, job := range p.jobs {
		job.cancel()
		// Settled cancellations can be retried only after an operator resume.
		// Failed gates remain durable in the existing gate-failure receipts.
		select {
		case <-job.done:
			if errors.Is(job.err, context.Canceled) {
				delete(p.jobs, key)
			}
		default:
		}
	}
}

func (manager *candidateWorkspaceManager) admissionGates(ctx context.Context, candidateKey, directory string, expected inspectedCandidateSource, required []string) ([]candidateGateReceipt, error) {
	binding, async := ctx.Value(candidateAdmissionContextKey{}).(candidateAdmissionContext)
	if !async {
		return manager.runGates(ctx, directory, required)
	}
	class := verification.Focused
	for _, id := range required {
		gate, found := manager.gates[id]
		if !found {
			return nil, errInvalidCandidateWorkspace
		}
		argv, heavy := verification.GoCommand(gate.Command)
		if heavy && verification.CommandClassAt(ctx, directory, argv) == verification.WholeModule {
			class = verification.WholeModule
		}
	}
	return manager.preflights.admit(ctx, candidateKey, class, binding, func(operation context.Context) ([]candidateGateReceipt, error) {
		operation = context.WithValue(operation, candidateGateIdentityContextKey{}, candidateGateIdentity{expected.commit, expected.tree})
		return manager.runGates(operation, directory, required)
	})
}
