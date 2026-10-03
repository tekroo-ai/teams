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
// effects before terminal cancellation. No production wiring selects it.
type Boundary struct {
	Control   agentruntime.RunControl
	Journal   agentruntime.Journal
	Configure SessionConfigurator
	Owner     string
	LeaseTTL  time.Duration
	Heartbeat time.Duration

	mu      sync.Mutex
	workers map[string]context.CancelCauseFunc
}

var _ application.AgentExecutionBoundary = (*Boundary)(nil)

var (
	errLeaseLost       = errors.New("native agent run lease lost")
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
	cancel := boundary.workers[conversationID]
	boundary.mu.Unlock()
	if cancel != nil {
		cancel(errCancelRequested)
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
	if err := boundary.reconcileBeforeTerminal(brief, oldDigest); err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if err := boundary.recordTerminalWithPrompt(brief, oldDigest, agentruntime.Cancelled, "execution brief superseded", false); err != nil {
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
	cancel := boundary.workers[string(brief.InvocationID)]
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
	if observed, terminal, err := observeJournal(brief, digest, entries); err != nil || terminal {
		return observed, err
	}
	lease, err := boundary.Control.Claim(ctx, string(brief.InvocationID), string(digest), boundary.Owner, time.Now().UTC(), boundary.LeaseTTL)
	if err != nil {
		return application.ExternalExecutionObservation{}, err
	}
	if !lease.Acquired {
		return nativeObservation(brief, digest, application.ExternalRunning, nil, false), nil
	}
	if lease.CancelRequested {
		if err := boundary.reconcileBeforeTerminal(brief, digest); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		if err := boundary.recordTerminal(brief, digest, agentruntime.Cancelled, "cancelled before resume"); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		boundary.release(brief, digest, lease.Epoch)
		entries, err := boundary.Journal.Load(ctx, string(brief.InvocationID))
		if err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		observed, _, err := observeJournal(brief, digest, entries)
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
		if err := boundary.reconcileBeforeTerminal(brief, digest); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		if err := boundary.recordTerminal(brief, digest, agentruntime.Cancelled, "cancelled before model use"); err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		boundary.release(brief, digest, lease.Epoch)
		entries, err := boundary.Journal.Load(ctx, string(brief.InvocationID))
		if err != nil {
			return application.ExternalExecutionObservation{}, err
		}
		observed, _, err := observeJournal(brief, digest, entries)
		return observed, err
	}
	// Prepare again in the worker so its binding is checked at the moment of
	// model use, not only during the short coordinator boundary call.
	deadlineCtx, stopDeadline := context.WithDeadline(context.Background(), brief.DeadlineAt)
	workerCtx, cancel := context.WithCancelCause(deadlineCtx)
	boundary.mu.Lock()
	if boundary.workers == nil {
		boundary.workers = make(map[string]context.CancelCauseFunc)
	}
	boundary.workers[string(brief.InvocationID)] = cancel
	boundary.mu.Unlock()
	go boundary.runWorker(workerCtx, cancel, stopDeadline, brief, digest, lease.Epoch)
	return nativeObservation(brief, digest, application.ExternalRunning, nil, false), nil
}

func (boundary *Boundary) runWorker(ctx context.Context, cancel context.CancelCauseFunc, stopDeadline context.CancelFunc, brief application.ExecutionBrief, digest kernel.Digest, epoch uint64) {
	defer cancel(nil)
	defer stopDeadline()
	defer func() {
		boundary.mu.Lock()
		delete(boundary.workers, string(brief.InvocationID))
		boundary.mu.Unlock()
	}()
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
	_, err = session.Run(ctx)
	stopHeartbeat()
	boundary.finishWorker(ctx, brief, digest, epoch, err)
}

func (boundary *Boundary) finishWorker(ctx context.Context, brief application.ExecutionBrief, digest kernel.Digest, epoch uint64, runErr error) {
	cause := context.Cause(ctx)
	if errors.Is(cause, errLeaseLost) {
		return
	}
	if runErr != nil || cause != nil {
		if err := boundary.reconcileBeforeTerminal(brief, digest); err != nil {
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
		if err := boundary.recordTerminal(brief, digest, kind, runErr.Error()); err != nil {
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
func (boundary *Boundary) reconcileBeforeTerminal(brief application.ExecutionBrief, digest kernel.Digest) error {
	ctx, cancel := context.WithTimeout(context.Background(), boundary.LeaseTTL)
	defer cancel()
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
	if err != nil || json.Unmarshal(entries[0].Payload, &start) != nil || start.RequestDigest != string(digest) || start.Prompt != string(encoded) {
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
				if call.Name == "write_file" || call.Name == "git_stage_files" || call.Name == "git_commit" {
					return ErrInvalidBinding
				}
			}
		}
		return nil
	}
	snapshot := *start.EffectAuthority
	if snapshot.WorkspaceRoot == "" || snapshot.Purpose != string(brief.Purpose) || snapshot.EffectPolicyDigest != string(brief.EffectPolicyDigest) || !slices.Equal(snapshot.Permissions, brief.RoleGrounding.Permissions) || !slices.Contains(snapshot.Permissions, "repository.edit") {
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

func (boundary *Boundary) recordTerminal(brief application.ExecutionBrief, digest kernel.Digest, kind agentruntime.Kind, reason string) error {
	return boundary.recordTerminalWithPrompt(brief, digest, kind, reason, true)
}

func (boundary *Boundary) recordTerminalWithPrompt(brief application.ExecutionBrief, digest kernel.Digest, kind agentruntime.Kind, reason string, strictPrompt bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), boundary.Heartbeat)
	defer cancel()
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
	if _, terminal, err := observeJournalWithPrompt(brief, digest, entries, strictPrompt); err != nil || terminal {
		return err
	}
	if len(reason) > 4096 {
		reason = reason[:4096]
	}
	payload, _ := json.Marshal(reason)
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
	if entries[0].Kind != agentruntime.Started || json.Unmarshal(entries[0].Payload, &start) != nil || start.RequestDigest != string(digest) || (strictPrompt && start.Prompt != string(encodedBrief)) {
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
		state, retryable = application.ExternalFailed, true
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
