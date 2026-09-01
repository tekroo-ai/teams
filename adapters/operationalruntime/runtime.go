package operationalruntime

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/adapters/executionruntime"
	"github.com/tekroo-ai/teams/adapters/filesystem"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

const workInvocationAuthorized = "WORK_INVOCATION_AUTHORIZED"

// Config contains the complete production dependency graph for operational
// task execution. The caller owns Store; Runtime owns only the intent feed it
// opens while assembling this graph.
type Config struct {
	Store                    *mongo.Store
	Catalogue                kernel.CatalogueSnapshot
	Clock                    kernel.Clock
	IDs                      kernel.IDSource
	OpenHandsBaseURL         string
	OpenHandsSessionAPIKey   string
	HTTPClient               *http.Client
	WorkspaceBindings        []openhands.WorkspaceBinding
	ExecutionProfiles        []openhands.ExecutionProfile
	RoleGrounding            application.RoleGroundingResolver
	OpenHandsPollInterval    time.Duration
	OpenHandsMaximumPages    uint32
	OpenHandsMaximumEvidence int
	EvidenceRoot             string
	ExecutionPolicy          application.OperationalExecutionPolicy
	EvidencePolicy           application.CommandEvidenceRecorderPolicy
	WorkerPolicy             executionruntime.Policy
}

// Runtime is the assembled Teams execution path: Mongo intent feed and lease,
// current Teams authority, kernel command handling, OpenHands execution,
// immutable evidence, and durable reconciliation.
type Runtime struct {
	catalogue   kernel.CatalogueSnapshot
	handler     *application.Handler
	coordinator *application.OperationalExecutionCoordinator
	worker      *executionruntime.Worker
	feed        *mongo.IntentFeed
	evidence    *filesystem.ExecutionEvidenceStore
	closeOnce   sync.Once
	closeErr    error
}

func New(ctx context.Context, config Config) (*Runtime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Store == nil || config.Catalogue == nil || config.Clock == nil || config.IDs == nil || config.RoleGrounding == nil || config.ExecutionPolicy.ConsumerID == "" || config.ExecutionPolicy.ConsumerID != config.WorkerPolicy.ConsumerID {
		return nil, application.ErrInvalidConfiguration
	}
	handler, err := application.NewHandler(config.Store, kernel.Evaluator{Catalogue: config.Catalogue}, config.Clock, config.IDs)
	if err != nil {
		return nil, err
	}
	workspaces, err := openhands.NewBoundWorkspaceResolver(config.WorkspaceBindings)
	if err != nil {
		return nil, err
	}
	profiles, err := openhands.NewBoundExecutionProfileResolver(config.ExecutionProfiles)
	if err != nil {
		return nil, err
	}
	client, err := openhands.NewClient(openhands.Config{
		BaseURL: config.OpenHandsBaseURL, SessionAPIKey: config.OpenHandsSessionAPIKey,
		HTTPClient: config.HTTPClient, Workspaces: workspaces, Profiles: profiles,
		PollInterval: config.OpenHandsPollInterval, MaximumPages: config.OpenHandsMaximumPages,
		MaximumEvidenceBytes: config.OpenHandsMaximumEvidence,
	})
	if err != nil {
		return nil, err
	}
	blobs, err := filesystem.NewExecutionEvidenceStore(config.EvidenceRoot)
	if err != nil {
		return nil, err
	}
	recorder, err := application.NewCommandEvidenceRecorder(handler, blobs, config.EvidencePolicy)
	if err != nil {
		return nil, err
	}
	coordinator, err := application.NewOperationalExecutionCoordinator(config.Store, handler, client, recorder, config.RoleGrounding, config.Clock, config.ExecutionPolicy)
	if err != nil {
		return nil, err
	}
	feed, err := config.Store.OpenIntentFeedForKind(ctx, config.WorkerPolicy.ConsumerID, workInvocationAuthorized)
	if err != nil {
		return nil, err
	}
	worker, err := executionruntime.NewWorker(executionruntime.MongoIntentSource{Feed: feed}, executionruntime.MongoIntentLeaser{Store: config.Store}, coordinator, systemClockAdapter{config.Clock}, config.WorkerPolicy)
	if err != nil {
		_ = feed.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return &Runtime{catalogue: config.Catalogue, handler: handler, coordinator: coordinator, worker: worker, feed: feed, evidence: blobs}, nil
}

func (runtime *Runtime) ReadExecutionOutput(ctx context.Context, digest kernel.Digest) ([]byte, error) {
	if runtime == nil || runtime.evidence == nil {
		return nil, application.ErrInvalidConfiguration
	}
	return runtime.evidence.Read(ctx, digest)
}

func (runtime *Runtime) Run(ctx context.Context) error {
	if runtime == nil || runtime.worker == nil {
		return application.ErrInvalidConfiguration
	}
	return runtime.worker.Run(ctx)
}

func (runtime *Runtime) ProcessOne(ctx context.Context, intent kernel.OutboxIntent) error {
	if runtime == nil || runtime.worker == nil {
		return application.ErrInvalidConfiguration
	}
	return runtime.worker.ProcessOne(ctx, intent)
}

func (runtime *Runtime) Handle(ctx context.Context, command kernel.KernelCommand, provenance kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	if runtime == nil || runtime.handler == nil {
		return kernel.CommandReceipt{}, application.ErrInvalidConfiguration
	}
	return runtime.handler.Handle(ctx, command, provenance)
}

func (runtime *Runtime) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.closeOnce.Do(func() {
		if runtime.feed != nil {
			runtime.closeErr = runtime.feed.Close(ctx)
		}
	})
	return runtime.closeErr
}

type systemClockAdapter struct{ kernel.Clock }

func (adapter systemClockAdapter) Now() time.Time { return adapter.Clock.Now() }
