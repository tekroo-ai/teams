package executionruntime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var ErrInvalidConfiguration = errors.New("invalid execution worker configuration")

type IntentSource interface {
	Next(context.Context) (kernel.OutboxIntent, error)
}

type IntentClaim struct {
	Intent     kernel.OutboxIntent
	ClaimEpoch uint64
}

type IntentLeaser interface {
	Acquire(context.Context, kernel.UUIDv7, string, time.Time, time.Duration) (IntentClaim, error)
	Extend(context.Context, kernel.UUIDv7, string, uint64, time.Time, time.Duration) error
	Resolve(context.Context, kernel.UUIDv7, string, uint64, time.Time, string) error
	Yield(context.Context, kernel.UUIDv7, string, uint64, time.Time) error
}

type InvocationProcessor interface {
	Process(context.Context, kernel.OutboxIntent) (application.OperationalExecutionResult, error)
}

type Clock interface{ Now() time.Time }

type Policy struct {
	ConsumerID                   string
	LeaseDuration                time.Duration
	ReconciliationInterval       time.Duration
	MaximumReconciliations       uint32
	MaximumConcurrentInvocations uint32
	LeaseOperationTimeout        time.Duration
}

func (policy Policy) valid() bool {
	return policy.ConsumerID != "" && len(policy.ConsumerID) <= 256 && policy.LeaseDuration > 0 && policy.ReconciliationInterval > 0 && policy.ReconciliationInterval < policy.LeaseDuration && policy.MaximumReconciliations > 0 && policy.MaximumConcurrentInvocations > 0 && policy.MaximumConcurrentInvocations <= 64 && policy.LeaseOperationTimeout > 0
}

type Worker struct {
	source    IntentSource
	leaser    IntentLeaser
	processor InvocationProcessor
	clock     Clock
	policy    Policy
	control   *Control
}

type ControlState string

const (
	ControlRunning ControlState = "RUNNING"
	ControlPaused  ControlState = "PAUSED"
)

type Status struct {
	State     ControlState
	Active    uint32
	Completed uint64
	LastError string
}

// Control gates admission of new invocations without cancelling work that is
// already in flight. It is intentionally separate from durable invocation
// cancellation, which remains a Teams domain command.
type Control struct {
	mu        sync.Mutex
	paused    bool
	changed   chan struct{}
	active    uint32
	completed uint64
	lastError string
}

func NewControl() *Control { return &Control{changed: make(chan struct{})} }

func (control *Control) Pause() {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.paused = true
}

func (control *Control) Resume() {
	control.mu.Lock()
	defer control.mu.Unlock()
	if !control.paused {
		return
	}
	control.paused = false
	close(control.changed)
	control.changed = make(chan struct{})
}

func (control *Control) Status() Status {
	control.mu.Lock()
	defer control.mu.Unlock()
	state := ControlRunning
	if control.paused {
		state = ControlPaused
	}
	return Status{State: state, Active: control.active, Completed: control.completed, LastError: control.lastError}
}

func (control *Control) wait(ctx context.Context) error {
	for {
		control.mu.Lock()
		if !control.paused {
			control.mu.Unlock()
			return nil
		}
		changed := control.changed
		control.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (control *Control) started() {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.active++
}

func (control *Control) finished(err error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	control.active--
	control.completed++
	if err != nil {
		control.lastError = err.Error()
	}
}

func NewWorker(source IntentSource, leaser IntentLeaser, processor InvocationProcessor, clock Clock, policy Policy) (*Worker, error) {
	if source == nil || leaser == nil || processor == nil || clock == nil || !policy.valid() {
		return nil, ErrInvalidConfiguration
	}
	return &Worker{source: source, leaser: leaser, processor: processor, clock: clock, policy: policy, control: NewControl()}, nil
}

func (worker *Worker) Pause() { worker.control.Pause() }

func (worker *Worker) Resume() { worker.control.Resume() }

func (worker *Worker) Status() Status { return worker.control.Status() }

func (worker *Worker) Run(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	results := make(chan error, worker.policy.MaximumConcurrentInvocations)
	active := uint32(0)
	waitOne := func() error {
		err := <-results
		active--
		return worker.runProcessResult(runCtx, err)
	}
	for {
		if active >= worker.policy.MaximumConcurrentInvocations {
			if err := waitOne(); err != nil {
				cancelRun()
				for active > 0 {
					<-results
					active--
				}
				return err
			}
			continue
		}
		select {
		case err := <-results:
			active--
			if runErr := worker.runProcessResult(runCtx, err); runErr != nil {
				cancelRun()
				for active > 0 {
					<-results
					active--
				}
				return runErr
			}
			continue
		default:
		}
		if err := worker.control.wait(runCtx); err != nil {
			cancelRun()
			for active > 0 {
				<-results
				active--
			}
			return err
		}
		sourceCtx, sourceCancel := context.WithTimeout(runCtx, worker.policy.LeaseOperationTimeout)
		intent, err := worker.source.Next(sourceCtx)
		sourceCancel()
		if err != nil {
			if (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, mongo.ErrIntentNotFound)) && runCtx.Err() == nil {
				continue
			}
			cancelRun()
			for active > 0 {
				<-results
				active--
			}
			return err
		}
		if intent.Kind != "WORK_INVOCATION_AUTHORIZED" {
			continue
		}
		if err := worker.control.wait(runCtx); err != nil {
			return err
		}
		active++
		worker.control.started()
		go func() {
			err := worker.ProcessOne(runCtx, intent)
			worker.control.finished(err)
			results <- err
		}()
	}
}

func (worker *Worker) runProcessResult(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, application.ErrExternalOutcomeUnknown) || errors.Is(err, mongo.ErrIntentNotFound) || errors.Is(err, mongo.ErrStaleClaim) {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ctx.Err()
	}
	return err
}

func (worker *Worker) ProcessOne(ctx context.Context, intent kernel.OutboxIntent) error {
	if !intent.IntentID.Valid() || !intent.EventID.Valid() || intent.Kind != "WORK_INVOCATION_AUTHORIZED" {
		return application.ErrInvalidOperationalExecution
	}
	leaseCtx, cancel := context.WithTimeout(ctx, worker.policy.LeaseOperationTimeout)
	claim, err := worker.leaser.Acquire(leaseCtx, intent.IntentID, worker.policy.ConsumerID, worker.clock.Now(), worker.policy.LeaseDuration)
	cancel()
	if err != nil {
		return err
	}
	if claim.Intent != intent || claim.ClaimEpoch == 0 {
		return application.ErrInvalidOperationalExecution
	}
	for attempt := uint32(0); attempt < worker.policy.MaximumReconciliations; attempt++ {
		processCtx, processCancel := context.WithTimeout(ctx, worker.policy.LeaseDuration)
		result, processErr := worker.processor.Process(processCtx, intent)
		processCancel()
		if processErr == nil && result.State.Terminal() {
			return worker.resolve(ctx, claim, "TERMINAL_"+string(result.State))
		}
		if errors.Is(processErr, application.ErrStaleWorkInvocation) || errors.Is(processErr, application.ErrInvalidOperationalExecution) {
			return worker.resolve(ctx, claim, "REJECTED_STALE_OR_INVALID_AUTHORITY")
		}
		if processErr != nil && !errors.Is(processErr, application.ErrExternalOutcomeUnknown) {
			return worker.yield(ctx, claim, processErr)
		}
		extendCtx, extendCancel := context.WithTimeout(ctx, worker.policy.LeaseOperationTimeout)
		err := worker.leaser.Extend(extendCtx, intent.IntentID, worker.policy.ConsumerID, claim.ClaimEpoch, worker.clock.Now(), worker.policy.LeaseDuration)
		extendCancel()
		if err != nil {
			return err
		}
		if err := wait(ctx, worker.policy.ReconciliationInterval); err != nil {
			return worker.yield(context.WithoutCancel(ctx), claim, err)
		}
	}
	return worker.yield(ctx, claim, application.ErrExternalOutcomeUnknown)
}

func (worker *Worker) resolve(ctx context.Context, claim IntentClaim, resolution string) error {
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), worker.policy.LeaseOperationTimeout)
	defer cancel()
	return worker.leaser.Resolve(operationCtx, claim.Intent.IntentID, worker.policy.ConsumerID, claim.ClaimEpoch, worker.clock.Now(), resolution)
}

func (worker *Worker) yield(ctx context.Context, claim IntentClaim, cause error) error {
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), worker.policy.LeaseOperationTimeout)
	defer cancel()
	if err := worker.leaser.Yield(operationCtx, claim.Intent.IntentID, worker.policy.ConsumerID, claim.ClaimEpoch, worker.clock.Now()); err != nil {
		return err
	}
	return cause
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type MongoIntentSource struct{ Feed *mongo.IntentFeed }

func (source MongoIntentSource) Next(ctx context.Context) (kernel.OutboxIntent, error) {
	if source.Feed == nil || source.Feed.Kind() != "WORK_INVOCATION_AUTHORIZED" {
		return kernel.OutboxIntent{}, ErrInvalidConfiguration
	}
	claimed, err := source.Feed.Poll(ctx)
	return claimed.Intent, err
}

type MongoIntentLeaser struct{ Store *mongo.Store }

func (leaser MongoIntentLeaser) Acquire(ctx context.Context, id kernel.UUIDv7, holder string, now time.Time, lease time.Duration) (IntentClaim, error) {
	if leaser.Store == nil {
		return IntentClaim{}, ErrInvalidConfiguration
	}
	claimed, err := leaser.Store.AcquireIntent(ctx, id, holder, now, lease)
	if err != nil {
		return IntentClaim{}, err
	}
	return IntentClaim{Intent: claimed.Intent, ClaimEpoch: claimed.ClaimEpoch}, nil
}

func (leaser MongoIntentLeaser) Extend(ctx context.Context, id kernel.UUIDv7, holder string, epoch uint64, now time.Time, lease time.Duration) error {
	if leaser.Store == nil {
		return ErrInvalidConfiguration
	}
	return leaser.Store.ExtendIntent(ctx, id, holder, epoch, now, lease)
}

func (leaser MongoIntentLeaser) Resolve(ctx context.Context, id kernel.UUIDv7, holder string, epoch uint64, now time.Time, resolution string) error {
	if leaser.Store == nil {
		return ErrInvalidConfiguration
	}
	return leaser.Store.ResolveIntent(ctx, id, holder, epoch, now, resolution)
}

func (leaser MongoIntentLeaser) Yield(ctx context.Context, id kernel.UUIDv7, holder string, epoch uint64, now time.Time) error {
	if leaser.Store == nil {
		return ErrInvalidConfiguration
	}
	return leaser.Store.YieldIntent(ctx, id, holder, epoch, now)
}

var (
	_ IntentSource = MongoIntentSource{}
	_ IntentLeaser = MongoIntentLeaser{}
)
