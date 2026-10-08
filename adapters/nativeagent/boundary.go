package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type SessionConfigurator func(context.Context, application.ExecutionBrief) (Config, error)

// Boundary is an opt-in AgentExecutionBoundary. It uses a MongoDB lease and
// journal to recover after daemon restart, and reconciles admitted filesystem
// effects before terminal cancellation.
type Boundary struct {
	Control   agentruntime.RunControl
	Journal   agentruntime.Journal
	Configure SessionConfigurator
	Owner     string
	LeaseTTL  time.Duration
	Heartbeat time.Duration

	mu      sync.Mutex
	workers map[string]*nativeWorker
}

type nativeWorker struct {
	cancel context.CancelCauseFunc
	epoch  uint64
}

var _ application.AgentExecutionBoundary = (*Boundary)(nil)

var (
	errLeaseLost       = agentruntime.ErrLeaseLost
	errCancelRequested = errors.New("native agent cancellation requested")
)

func (boundary *Boundary) Start(ctx context.Context, brief application.ExecutionBrief, digest kernel.Digest) (application.ExternalExecutionObservation, error) {
	return boundary.ensure(ctx, brief, digest)
}

// Reconciliation may safely start an absent read-only worker: its MongoDB
// claim and journal sequence prevent two accepted transcripts.
func (boundary *Boundary) ReconcileStart(ctx context.Context, brief application.ExecutionBrief, digest kernel.Digest) (application.ExternalExecutionObservation, error) {
	return boundary.ensure(ctx, brief, digest)
}

func (boundary *Boundary) Inspect(ctx context.Context, brief application.ExecutionBrief, conversationID string, digest kernel.Digest) (application.ExternalExecutionObservation, error) {
	if conversationID != string(brief.InvocationID) {
		return application.ExternalExecutionObservation{}, ErrInvalidBinding
	}
	return boundary.ensure(ctx, brief, digest)
}

func (boundary *Boundary) ReconcileSuperseded(ctx context.Context, brief application.ExecutionBrief, conversationID string, oldDigest, newDigest kernel.Digest) (application.ExternalExecutionObservation, error) {
	if conversationID != string(brief.InvocationID) || !validBoundaryRequest(brief, newDigest) || !oldDigest.Valid() || oldDigest == newDigest || boundary.Control == nil || boundary.Journal == nil || boundary.Owner == "" || boundary.LeaseTTL <= 0 || boundary.Heartbeat <= 0 {
		return application.ExternalExecutionObservation{}, ErrInvalidBinding
	}
	if err := boundary.Control.RequestCancel(ctx, conversationID, string(oldDigest)); err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	boundary.mu.Lock()
	worker := boundary.workers[conversationID]
	var cancel context.CancelCauseFunc
	if worker != nil {
		cancel = worker.cancel
	}
	boundary.mu.Unlock()
	if cancel != nil {
		cancel(errCancelRequested)
	}
	if worker != nil {
		return application.ExternalExecutionObservation{}, errLeaseLost
	}
	entries, err := boundary.Journal.Load(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if observed, terminal, err := observeJournalWithPrompt(brief, oldDigest, entries, false); err != nil || terminal {
		return observed, err
	}
	lease, err := boundary.Control.Claim(ctx, conversationID, string(oldDigest), boundary.Owner, time.Now().UTC(), boundary.LeaseTTL)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if !lease.Acquired {
		// The old worker must first finish or lose its lease. A RUNNING result
		// here would let the coordinator close the old attempt prematurely.
		return application.ExternalExecutionObservation{}, errLeaseLost
	}
	if err := boundary.reconcileBeforeTerminal(brief, oldDigest, lease.Epoch); err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if err := boundary.recordTerminalWithPrompt(brief, oldDigest, lease.Epoch, agentruntime.Cancelled, "execution brief superseded", false); err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	boundary.release(brief, oldDigest, lease.Epoch)
	entries, err = boundary.Journal.Load(ctx, conversationID)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	observed, _, err := observeJournalWithPrompt(brief, oldDigest, entries, false)
	return observed, err
}

func (boundary *Boundary) Cancel(ctx context.Context, brief application.ExecutionBrief, conversationID string, digest kernel.Digest) (application.ExternalExecutionObservation, error) {
	if conversationID != string(brief.InvocationID) || !validBoundaryRequest(brief, digest) || boundary.Control == nil || boundary.Journal == nil {
		return application.ExternalExecutionObservation{}, ErrInvalidBinding
	}
	if err := boundary.Control.RequestCancel(ctx, string(brief.InvocationID), string(digest)); err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	boundary.mu.Lock()
	worker := boundary.workers[string(brief.InvocationID)]
	var cancel context.CancelCauseFunc
	if worker != nil {
		cancel = worker.cancel
	}
	boundary.mu.Unlock()
	if cancel != nil {
		cancel(errCancelRequested)
	}
	return boundary.ensure(ctx, brief, digest)
}

func (boundary *Boundary) ensure(ctx context.Context, brief application.ExecutionBrief, digest kernel.Digest) (application.ExternalExecutionObservation, error) {
	if !validBoundaryRequest(brief, digest) || boundary.Control == nil || boundary.Journal == nil || boundary.Configure == nil || boundary.Owner == "" || boundary.LeaseTTL <= 0 || boundary.Heartbeat <= 0 || boundary.Heartbeat >= boundary.LeaseTTL {
		return application.ExternalExecutionObservation{}, ErrInvalidBinding
	}
	entries, err := boundary.Journal.Load(ctx, string(brief.InvocationID))
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if observed, terminal, err := boundary.observeJournal(ctx, brief, digest, entries); err != nil || terminal {
		return observed, err
	}
	// Reserve the local slot BEFORE Claim/Configure. In particular, waking
	// after lease expiry must drain the previous goroutine before re-claiming;
	// replacing its cancel function would lose the ability to stop it.
	id := string(brief.InvocationID)
	boundary.mu.Lock()
	if previous := boundary.workers[id]; previous != nil {
		cancel, epoch := previous.cancel, previous.epoch
		boundary.mu.Unlock()
		if cancel != nil {
			renewCtx, stop := context.WithTimeout(ctx, boundary.Heartbeat)
			renewal, err := boundary.Control.Renew(renewCtx, id, string(digest), boundary.Owner, epoch, time.Now().UTC(), boundary.LeaseTTL)
			stop()
			if err != nil || !renewal.Held {
				cancel(errLeaseLost)
			} else if renewal.CancelRequested {
				cancel(errCancelRequested)
			}
		}
		return nativeObservation(brief, digest, application.ExternalRunning, nil, false), nil
	}
	if boundary.workers == nil {
		boundary.workers = make(map[string]*nativeWorker)
	}
	worker := &nativeWorker{}
	boundary.workers[id] = worker
	boundary.mu.Unlock()
	started := false
	defer func() {
		if !started {
			boundary.removeWorker(id, worker)
		}
	}()
	lease, err := boundary.Control.Claim(ctx, string(brief.InvocationID), string(digest), boundary.Owner, time.Now().UTC(), boundary.LeaseTTL)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if !lease.Acquired {
		return nativeObservation(brief, digest, application.ExternalRunning, nil, false), nil
	}
	if lease.CancelRequested {
		if err := boundary.reconcileBeforeTerminal(brief, digest, lease.Epoch); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		if err := boundary.recordTerminal(brief, digest, lease.Epoch, agentruntime.Cancelled, "cancelled before resume"); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		boundary.release(brief, digest, lease.Epoch)
		entries, err := boundary.Journal.Load(ctx, string(brief.InvocationID))
		if err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		observed, _, err := boundary.observeJournal(ctx, brief, digest, entries)
		return observed, err
	}
	config, err := boundary.Configure(ctx, brief)
	if err == nil {
		config.Journal = boundary.Journal
		_, err = prepareSession(ctx, brief, digest, config)
	}
	if err != nil {
		boundary.release(brief, digest, lease.Epoch)
		return nativeObservation(brief, digest, application.ExternalRejected, []byte(err.Error()), false), nil
	}
	renewCtx, stopRenew := context.WithTimeout(ctx, boundary.Heartbeat)
	renewal, err := boundary.Control.Renew(renewCtx, string(brief.InvocationID), string(digest), boundary.Owner, lease.Epoch, time.Now().UTC(), boundary.LeaseTTL)
	stopRenew()
	if err != nil || !renewal.Held {
		return application.ExternalExecutionObservation{}, errors.Join(errLeaseLost, err)
	}
	if renewal.CancelRequested {
		if err := boundary.reconcileBeforeTerminal(brief, digest, lease.Epoch); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		if err := boundary.recordTerminal(brief, digest, lease.Epoch, agentruntime.Cancelled, "cancelled before model use"); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		boundary.release(brief, digest, lease.Epoch)
		entries, err := boundary.Journal.Load(ctx, string(brief.InvocationID))
		if err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		observed, _, err := boundary.observeJournal(ctx, brief, digest, entries)
		return observed, err
	}
	// Prepare again in the worker so its binding is checked at the moment of
	// model use, not only during the short coordinator boundary call.
	deadlineCtx, stopDeadline := context.WithDeadline(context.Background(), brief.DeadlineAt)
	workerCtx, cancel := context.WithCancelCause(deadlineCtx)
	workerCtx = agentruntime.WithRunOwnership(workerCtx, boundary.ownership(brief, digest, lease.Epoch))
	boundary.mu.Lock()
	worker.cancel, worker.epoch = cancel, lease.Epoch
	boundary.mu.Unlock()
	started = true
	go boundary.runWorker(workerCtx, cancel, stopDeadline, brief, digest, worker)
	return nativeObservation(brief, digest, application.ExternalRunning, nil, false), nil
}

func (boundary *Boundary) runWorker(ctx context.Context, cancel context.CancelCauseFunc, stopDeadline context.CancelFunc, brief application.ExecutionBrief, digest kernel.Digest, worker *nativeWorker) {
	epoch := worker.epoch
	defer cancel(nil)
	defer stopDeadline()
	defer boundary.removeWorker(string(brief.InvocationID), worker)
	stopRenewal := make(chan struct{})
	renewalDone := make(chan struct{})
	go boundary.renewWhileRunning(ctx, cancel, brief, digest, epoch, stopRenewal, renewalDone)
	var renewalOnce sync.Once
	stopHeartbeat := func() {
		renewalOnce.Do(func() {
			close(stopRenewal)
			<-renewalDone
		})
	}
	defer stopHeartbeat()
	configCtx, configCancel := context.WithTimeout(ctx, boundary.Heartbeat)
	config, err := boundary.Configure(configCtx, brief)
	configCancel()
	if err != nil {
		stopHeartbeat()
		boundary.finishWorker(ctx, brief, digest, epoch, err)
		return
	}
	config.Journal = boundary.Journal
	bindCtx, bindCancel := context.WithTimeout(ctx, boundary.Heartbeat)
	session, err := prepareSession(bindCtx, brief, digest, config)
	bindCancel()
	if err != nil {
		stopHeartbeat()
		boundary.finishWorker(ctx, brief, digest, epoch, err)
		return
	}
	session.Runner.CheckOwnership = func(operation context.Context) error {
		checkCtx, stop := context.WithTimeout(operation, boundary.Heartbeat)
		defer stop()
		renewal, err := boundary.Control.Renew(checkCtx, string(brief.InvocationID), string(digest), boundary.Owner, epoch, time.Now().UTC(), boundary.LeaseTTL)
		if err != nil || !renewal.Held {
			cancel(errLeaseLost)
			return errors.Join(errLeaseLost, err)
		}
		if renewal.CancelRequested {
			cancel(errCancelRequested)
			return errCancelRequested
		}
		return nil
	}
	_, err = session.Run(ctx)
	stopHeartbeat()
	boundary.finishWorker(ctx, brief, digest, epoch, err)
}

func (boundary *Boundary) finishWorker(ctx context.Context, brief application.ExecutionBrief, digest kernel.Digest, epoch uint64, runErr error) {
	cause := context.Cause(ctx)
	if errors.Is(cause, errLeaseLost) || errors.Is(runErr, errLeaseLost) {
		// Release is itself epoch-conditional. It cannot release a replacement
		// owner's lease, and avoids waiting a full TTL after a local drain.
		boundary.release(brief, digest, epoch)
		return
	}
	if runErr != nil || cause != nil {
		// A deadline/error can arrive before the renewal goroutine runs on
		// wake. Do not publish it for an expired or superseded owner. The
		// journal adapter also checks this atomically at the actual append.
		checkCtx, stop := context.WithTimeout(context.Background(), boundary.Heartbeat)
		renewal, err := boundary.Control.Renew(checkCtx, string(brief.InvocationID), string(digest), boundary.Owner, epoch, time.Now().UTC(), boundary.LeaseTTL)
		stop()
		if err != nil || !renewal.Held {
			boundary.release(brief, digest, epoch)
			return
		}
		if err := boundary.reconcileBeforeTerminal(brief, digest, epoch); err != nil {
			// Do not report cancellation/failure while a physical effect is
			// uncertain. A later inspector may reclaim and reconcile it.
			return
		}
	}
	if errors.Is(runErr, agentruntime.ErrEffectUncertain) && cause == nil {
		boundary.release(brief, digest, epoch)
		return
	}
	if runErr != nil {
		kind := agentruntime.Failed
		switch {
		case errors.Is(cause, context.DeadlineExceeded):
			kind = agentruntime.TimedOut
		case errors.Is(cause, errCancelRequested):
			kind = agentruntime.Cancelled
		}
		payload, _ := json.Marshal(runErr.Error())
		if kind == agentruntime.Failed {
			payload, _ = json.Marshal(agentruntime.ClassifyFailure(runErr))
		}
		if err := boundary.recordTerminalPayload(brief, digest, epoch, kind, payload, true); err != nil {
			// Keep the lease: a later inspector can reconcile the uncertain write
			// after expiry instead of claiming that a terminal result exists.
			return
		}
	}
	boundary.release(brief, digest, epoch)
}

func prepareSession(ctx context.Context, brief application.ExecutionBrief, digest kernel.Digest, config Config) (Session, error) {
	if config.Effects != nil {
		return PrepareWithEffects(ctx, brief, digest, config)
	}
	return PrepareReadOnly(ctx, brief, digest, config)
}

// Called only while this boundary owns the invocation lease or after its
// worker has returned. It cannot start a new effect or a model turn.
func (boundary *Boundary) reconcileBeforeTerminal(brief application.ExecutionBrief, digest kernel.Digest, epoch uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), boundary.LeaseTTL)
	defer cancel()
	ctx = agentruntime.WithRunOwnership(ctx, boundary.ownership(brief, digest, epoch))
	entries, err := boundary.Journal.Load(ctx, string(brief.InvocationID))
	if err != nil || len(entries) == 0 || entries[len(entries)-1].Kind == agentruntime.Started {
		return err
	}
	var start struct {
		RequestDigest   string                        `json:"request_digest"`
		Prompt          string                        `json:"prompt"`
		EffectAuthority *agentruntime.EffectAuthority `json:"effect_authority"`
	}
	encoded, err := json.Marshal(brief)
	if err != nil || json.Unmarshal(entries[0].Payload, &start) != nil || start.RequestDigest != string(digest) || !nativePromptMatchesBrief(brief, digest, encoded, start.Prompt) {
		return ErrInvalidBinding
	}
	if start.EffectAuthority == nil {
		for _, entry := range entries[1:] {
			if entry.Kind != agentruntime.ModelTurn {
				continue
			}
			var turn agentruntime.Completion
			if json.Unmarshal(entry.Payload, &turn) != nil {
				return ErrInvalidBinding
			}
			for _, call := range turn.ToolCalls {
				if (agenttools.MutationTurnAdapter{}).Handles(call.Name) {
					return ErrInvalidBinding
				}
			}
		}
		return nil
	}
	snapshot := *start.EffectAuthority
	// Test execution also reserves effects. Its read-only validator must be
	// able to drain those receipts and publish failure/cancellation/timeout.
	admittedEffects := slices.Contains(snapshot.Permissions, "repository.edit") || slices.Contains(snapshot.Permissions, "test.execute")
	if snapshot.WorkspaceRoot == "" || snapshot.Purpose != string(brief.Purpose) || snapshot.EffectPolicyDigest != string(brief.EffectPolicyDigest) || !slices.Equal(snapshot.Permissions, brief.RoleGrounding.Permissions) || !admittedEffects {
		return ErrInvalidBinding
	}
	config, err := boundary.Configure(ctx, brief)
	if err != nil {
		return err
	}
	config.Journal = boundary.Journal
	// The live authority source correctly rejects a cancelled or expired
	// invocation. Reconciliation is read-only: use the server-recorded
	// admission snapshot solely for inspecting effects already reserved.
	bound := staticReconciliationBinding{authority: agenttools.Authority{WorkspaceRoot: snapshot.WorkspaceRoot,
		Permissions: slices.Clone(snapshot.Permissions), Purpose: brief.Purpose, EffectPolicyDigest: brief.EffectPolicyDigest}}
	config.Bindings = bound
	config.Gateway.Bindings = bound
	session, err := PrepareWithEffects(ctx, brief, digest, config)
	if err != nil {
		return err
	}
	return session.Runner.ReconcilePendingEffects(ctx, session.InvocationID, session.RequestDigest, session.Prompt)
}

type staticReconciliationBinding struct{ authority agenttools.Authority }

func (binding staticReconciliationBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (agenttools.Authority, error) {
	return binding.authority, nil
}

func (boundary *Boundary) renewWhileRunning(ctx context.Context, cancel context.CancelCauseFunc, brief application.ExecutionBrief, digest kernel.Digest, epoch uint64, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(boundary.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, stop := context.WithTimeout(context.Background(), boundary.Heartbeat)
			renewal, err := boundary.Control.Renew(renewCtx, string(brief.InvocationID), string(digest), boundary.Owner, epoch, time.Now().UTC(), boundary.LeaseTTL)
			stop()
			if err != nil || !renewal.Held {
				cancel(errLeaseLost)
				return
			}
			if renewal.CancelRequested {
				cancel(errCancelRequested)
				return
			}
		}
	}
}

func (boundary *Boundary) recordTerminal(brief application.ExecutionBrief, digest kernel.Digest, epoch uint64, kind agentruntime.Kind, reason string) error {
	return boundary.recordTerminalWithPrompt(brief, digest, epoch, kind, reason, true)
}

func (boundary *Boundary) recordTerminalWithPrompt(brief application.ExecutionBrief, digest kernel.Digest, epoch uint64, kind agentruntime.Kind, reason string, strictPrompt bool) error {
	if len(reason) > 4096 {
		reason = reason[:4096]
	}
	payload, _ := json.Marshal(reason)
	return boundary.recordTerminalPayload(brief, digest, epoch, kind, payload, strictPrompt)
}

func (boundary *Boundary) recordTerminalPayload(brief application.ExecutionBrief, digest kernel.Digest, epoch uint64, kind agentruntime.Kind, payload json.RawMessage, strictPrompt bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), boundary.Heartbeat)
	defer cancel()
	ctx = agentruntime.WithRunOwnership(ctx, boundary.ownership(brief, digest, epoch))
	entries, err := boundary.Journal.Load(ctx, string(brief.InvocationID))
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		encodedBrief, err := json.Marshal(brief)
		if err != nil {
			return err
		}
		start, _ := json.Marshal(struct {
			RequestDigest string `json:"request_digest"`
			Prompt        string `json:"prompt"`
		}{string(digest), string(encodedBrief)})
		if _, err := boundary.Journal.Append(ctx, string(brief.InvocationID), 0, agentruntime.Started, start); err != nil {
			entries, err = boundary.Journal.Load(ctx, string(brief.InvocationID))
			if err != nil || len(entries) == 0 || entries[0].Kind != agentruntime.Started || string(entries[0].Payload) != string(start) {
				return errors.Join(agentruntime.ErrConflict, err)
			}
		} else {
			entries, err = boundary.Journal.Load(ctx, string(brief.InvocationID))
			if err != nil {
				return err
			}
		}
	}
	var terminal bool
	if strictPrompt {
		_, terminal, err = boundary.observeJournal(ctx, brief, digest, entries)
	} else {
		_, terminal, err = observeJournalWithPrompt(brief, digest, entries, false)
	}
	if err != nil || terminal {
		return err
	}
	if _, err := boundary.Journal.Append(ctx, string(brief.InvocationID), entries[len(entries)-1].Sequence, kind, payload); err != nil {
		current, loadErr := boundary.Journal.Load(ctx, string(brief.InvocationID))
		if loadErr != nil {
			return errors.Join(err, loadErr)
		}
		if len(current) == len(entries)+1 && current[len(entries)].Kind == kind && string(current[len(entries)].Payload) == string(payload) {
			return nil
		}
		return err
	}
	return nil
}

func (boundary *Boundary) ownership(brief application.ExecutionBrief, digest kernel.Digest, epoch uint64) agentruntime.RunOwnership {
	return agentruntime.RunOwnership{InvocationID: string(brief.InvocationID), RequestDigest: string(digest), Owner: boundary.Owner, Epoch: epoch}
}

func (boundary *Boundary) removeWorker(id string, worker *nativeWorker) {
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	if boundary.workers[id] == worker {
		delete(boundary.workers, id)
	}
}

func (boundary *Boundary) release(brief application.ExecutionBrief, digest kernel.Digest, epoch uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), boundary.Heartbeat)
	defer cancel()
	_ = boundary.Control.Release(ctx, string(brief.InvocationID), string(digest), boundary.Owner, epoch, time.Now().UTC())
}

func validBoundaryRequest(brief application.ExecutionBrief, digest kernel.Digest) bool {
	if !brief.InvocationID.Valid() || !digest.Valid() || brief.ContractManifest != kernel.ContractIdentity {
		return false
	}
	encoded, err := json.Marshal(brief)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(encoded)
	return string(digest) == hex.EncodeToString(sum[:])
}

func observeJournal(brief application.ExecutionBrief, digest kernel.Digest, entries []agentruntime.Entry) (application.ExternalExecutionObservation, bool, error) {
	return observeJournalWithPrompt(brief, digest, entries, true)
}

func (boundary *Boundary) observeJournal(ctx context.Context, brief application.ExecutionBrief, digest kernel.Digest, entries []agentruntime.Entry) (application.ExternalExecutionObservation, bool, error) {
	if len(entries) == 0 || brief.MessageHandler != nil || brief.ResultProtocol == nil {
		return observeJournal(brief, digest, entries)
	}
	var start struct {
		RequestDigest string `json:"request_digest"`
		Prompt        string `json:"prompt"`
	}
	encoded, err := json.Marshal(brief)
	if err != nil || entries[0].Kind != agentruntime.Started || json.Unmarshal(entries[0].Payload, &start) != nil || start.RequestDigest != string(digest) {
		return application.ExternalExecutionObservation{}, false, ErrInvalidBinding
	}
	if nativePromptMatchesBrief(brief, digest, encoded, start.Prompt) {
		return observeJournal(brief, digest, entries)
	}
	if boundary.Configure == nil {
		return application.ExternalExecutionObservation{}, false, ErrInvalidBinding
	}
	config, err := boundary.Configure(ctx, brief)
	if err != nil {
		return application.ExternalExecutionObservation{}, false, err
	}
	expected, err := nativeProtocolPrompt(brief, digest, config.ResultContract, config.ResultInput)
	if err != nil || expected != start.Prompt {
		return application.ExternalExecutionObservation{}, false, errors.Join(ErrInvalidBinding, err)
	}
	// Only prompt comparison has been satisfied by the exact server-resolved
	// constructor above. Sequence, invocation and terminal validation stay on.
	return observeJournalWithPrompt(brief, digest, entries, false)
}

func observeJournalWithPrompt(brief application.ExecutionBrief, digest kernel.Digest, entries []agentruntime.Entry, strictPrompt bool) (application.ExternalExecutionObservation, bool, error) {
	if len(entries) == 0 {
		return nativeObservation(brief, digest, application.ExternalRunning, nil, false), false, nil
	}
	encodedBrief, err := json.Marshal(brief)
	if err != nil {
		return application.ExternalExecutionObservation{}, false, err
	}
	var start struct {
		RequestDigest string `json:"request_digest"`
		Prompt        string `json:"prompt"`
	}
	if entries[0].Kind != agentruntime.Started || json.Unmarshal(entries[0].Payload, &start) != nil || start.RequestDigest != string(digest) || (strictPrompt && !nativePromptMatchesBrief(brief, digest, encodedBrief, start.Prompt)) {
		return application.ExternalExecutionObservation{}, false, ErrInvalidBinding
	}
	for index, entry := range entries {
		if entry.InvocationID != string(brief.InvocationID) || entry.Sequence != uint64(index+1) || !json.Valid(entry.Payload) {
			return application.ExternalExecutionObservation{}, false, ErrInvalidBinding
		}
	}
	last := entries[len(entries)-1]
	state := application.ExternalRunning
	var output []byte
	var retryable bool
	switch last.Kind {
	case agentruntime.Finished:
		state = application.ExternalSucceeded
	case agentruntime.Failed:
		failure, err := agentruntime.DecodeFailure(last.Payload)
		if err != nil {
			return application.ExternalExecutionObservation{}, false, ErrInvalidBinding
		}
		output, err = json.Marshal(failure)
		if err != nil {
			return application.ExternalExecutionObservation{}, false, err
		}
		return nativeObservation(brief, digest, application.ExternalFailed, output, failure.Retryable), true, nil
	case agentruntime.Cancelled:
		state = application.ExternalCancelled
	case agentruntime.TimedOut:
		state = application.ExternalTimedOut
	default:
		return nativeObservation(brief, digest, state, nil, false), false, nil
	}
	var text string
	if json.Unmarshal(last.Payload, &text) != nil {
		return application.ExternalExecutionObservation{}, false, ErrInvalidBinding
	}
	output = []byte(text)
	return nativeObservation(brief, digest, state, output, retryable), true, nil
}

func nativeObservation(brief application.ExecutionBrief, digest kernel.Digest, state application.ExternalExecutionState, output []byte, retryable bool) application.ExternalExecutionObservation {
	return application.ExternalExecutionObservation{
		InvocationID: brief.InvocationID, RequestDigest: digest,
		ConversationID: string(brief.InvocationID), State: state,
		ActorFQN: brief.ActorFQN, Execution: brief.Execution,
		ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest,
		WorkspaceID: brief.Scope.WorkspaceID, ToolPolicyDigest: brief.ToolPolicyDigest,
		EffectPolicyDigest: brief.EffectPolicyDigest, Retryable: retryable,
		Output: append([]byte(nil), output...),
	}
}
