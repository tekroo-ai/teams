package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

var (
	ErrDeadlineRequired    = errors.New("mongo operation requires a context deadline")
	ErrUnsupportedTopology = errors.New("mongo topology does not support transactions and change streams")
	ErrMetadataMismatch    = errors.New("mongo kernel metadata mismatch")
	ErrConflict            = errors.New("mongo decision precondition conflict")
	ErrInvalidDecision     = errors.New("invalid mongo decision")
	ErrCorruptAggregate    = errors.New("mongo aggregate snapshot contradicts event fold")
	ErrInjectedFault       = errors.New("injected mongo transaction fault")
)

type Config struct {
	URI                    string
	Database               string
	ContractIdentity       string
	ManifestSHA256         kernel.Digest
	MigrationLevel         uint64
	Policy                 kernel.AuthorizationPolicy
	BacklogLimit           int64
	DeliveryPolicyRevision uint64
	DeploymentIdentity     kernel.Digest
}

type Store struct {
	client *driver.Client
	db     *driver.Database
	owns   bool

	faultMu   sync.Mutex
	fault     string
	uncertain bool

	backlogLimit int64
}

type aggregateDocument struct {
	ID       string `bson:"_id"`
	Revision uint64 `bson:"revision"`
	State    []byte `bson:"state,omitempty"`
}

type eventDocument struct {
	ID            string `bson:"_id"`
	AggregateKey  string `bson:"aggregate_key"`
	Revision      uint64 `bson:"revision"`
	EventType     string `bson:"event_type"`
	Qualification string `bson:"qualification,omitempty"`
	Data          []byte `bson:"data"`
}

type receiptDocument struct {
	ID          string `bson:"_id"`
	Scope       string `bson:"scope"`
	Fingerprint string `bson:"fingerprint"`
	TargetKey   string `bson:"target_key"`
	Data        []byte `bson:"data"`
}

type valueDocument struct {
	ID   string `bson:"_id"`
	Data []byte `bson:"data"`
}

type executionDocument struct {
	ID           string `bson:"_id"`
	ExecutionID  string `bson:"execution_id"`
	FencingEpoch uint64 `bson:"fencing_epoch"`
}

type evidenceDocument struct {
	ID        string `bson:"_id"`
	SHA256    string `bson:"sha256"`
	Available bool   `bson:"available"`
}

type metadataDocument struct {
	ID               string `bson:"_id"`
	ContractIdentity string `bson:"contract_identity,omitempty"`
	ManifestSHA256   string `bson:"manifest_sha256,omitempty"`
	MigrationLevel   uint64 `bson:"migration_level,omitempty"`
	Data             []byte `bson:"data,omitempty"`
}

func Open(ctx context.Context, config Config) (*Store, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if config.URI == "" || config.Database == "" || config.ContractIdentity != kernel.ContractIdentity || !config.ManifestSHA256.Valid() || config.MigrationLevel == 0 || config.DeliveryPolicyRevision == 0 || config.DeploymentIdentity != "" && !config.DeploymentIdentity.Valid() {
		return nil, ErrMetadataMismatch
	}
	if config.BacklogLimit <= 0 {
		config.BacklogLimit = 10_000
	}
	client, err := driver.Connect(options.Client().ApplyURI(config.URI).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority()).
		SetRetryWrites(true))
	if err != nil {
		return nil, err
	}
	store := &Store{client: client, db: client.Database(config.Database), owns: true, backlogLimit: config.BacklogLimit}
	if config.DeploymentIdentity != "" {
		if err := store.validateDeploymentBeforeInitialization(ctx, config.DeploymentIdentity); err != nil {
			_ = client.Disconnect(context.Background())
			return nil, err
		}
	}
	if err := store.initialize(ctx, config); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	if config.DeploymentIdentity != "" {
		if err := store.BindDeploymentIdentity(ctx, config.DeploymentIdentity); err != nil {
			_ = client.Disconnect(context.Background())
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) Close(ctx context.Context) error {
	if !s.owns {
		return nil
	}
	return s.client.Disconnect(ctx)
}

func (s *Store) initialize(ctx context.Context, config Config) error {
	if err := s.client.Ping(ctx, readpref.Primary()); err != nil {
		return err
	}
	var hello struct {
		SetName               string `bson:"setName"`
		LogicalSessionTimeout *int64 `bson:"logicalSessionTimeoutMinutes"`
		IsWritablePrimary     bool   `bson:"isWritablePrimary"`
	}
	if err := s.db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return err
	}
	if hello.SetName == "" || hello.LogicalSessionTimeout == nil || !hello.IsWritablePrimary {
		return ErrUnsupportedTopology
	}
	if err := s.ensureIndexes(ctx); err != nil {
		return err
	}
	metadata := metadataDocument{
		ID: "kernel", ContractIdentity: config.ContractIdentity, ManifestSHA256: string(config.ManifestSHA256), MigrationLevel: config.MigrationLevel,
	}
	var existing metadataDocument
	err := s.db.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: "kernel"}}).Decode(&existing)
	switch {
	case errors.Is(err, driver.ErrNoDocuments):
		if _, err := s.db.Collection("metadata").InsertOne(ctx, metadata); err != nil && !driver.IsDuplicateKeyError(err) {
			return err
		}
	case err != nil:
		return err
	case existing.ContractIdentity != metadata.ContractIdentity || existing.ManifestSHA256 != metadata.ManifestSHA256 || existing.MigrationLevel != metadata.MigrationLevel:
		return ErrMetadataMismatch
	}
	policyBytes, err := encode(normalizePolicy(config.Policy))
	if err != nil {
		return err
	}
	policy := metadataDocument{ID: "authorization", Data: policyBytes}
	err = s.db.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: "authorization"}}).Decode(&existing)
	switch {
	case errors.Is(err, driver.ErrNoDocuments):
		if _, err := s.db.Collection("metadata").InsertOne(ctx, policy); err != nil && !driver.IsDuplicateKeyError(err) {
			return err
		}
	case err != nil:
		return err
	case !reflect.DeepEqual(existing.Data, policy.Data):
		return ErrMetadataMismatch
	}
	deliveryPolicyBytes, err := encode(struct {
		Revision     uint64 `json:"revision"`
		BacklogLimit int64  `json:"backlog_limit"`
	}{config.DeliveryPolicyRevision, config.BacklogLimit})
	if err != nil {
		return err
	}
	deliveryPolicy := metadataDocument{ID: "delivery-policy", Data: deliveryPolicyBytes}
	err = s.db.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: "delivery-policy"}}).Decode(&existing)
	switch {
	case errors.Is(err, driver.ErrNoDocuments):
		if _, err := s.db.Collection("metadata").InsertOne(ctx, deliveryPolicy); err != nil && !driver.IsDuplicateKeyError(err) {
			return err
		}
	case err != nil:
		return err
	case !reflect.DeepEqual(existing.Data, deliveryPolicy.Data):
		return ErrMetadataMismatch
	}
	return nil
}

func (s *Store) ensureIndexes(ctx context.Context) error {
	definitions := map[string][]driver.IndexModel{
		"events": {
			{Keys: bson.D{{Key: "aggregate_key", Value: 1}, {Key: "revision", Value: 1}}, Options: options.Index().SetName("aggregate_revision_unique").SetUnique(true)},
		},
		"receipts": {
			{Keys: bson.D{{Key: "scope", Value: 1}}, Options: options.Index().SetName("idempotency_scope_unique").SetUnique(true)},
		},
		"outbox": {
			{Keys: bson.D{{Key: "state", Value: 1}, {Key: "lease_until", Value: 1}}, Options: options.Index().SetName("delivery_state_lease")},
		},
		"identity_conflicts": {
			{Keys: bson.D{{Key: "command_id", Value: 1}, {Key: "observed_at", Value: 1}}, Options: options.Index().SetName("command_observed")},
		},
		"story_projections": {
			{Keys: bson.D{{Key: "phase", Value: 1}}, Options: options.Index().SetName("story_phase")},
		},
		"task_projections": {
			{Keys: bson.D{{Key: "story_id", Value: 1}, {Key: "phase", Value: 1}}, Options: options.Index().SetName("task_story_phase")},
			{Keys: bson.D{{Key: "owner_fqn", Value: 1}, {Key: "phase", Value: 1}}, Options: options.Index().SetName("task_owner_phase")},
		},
	}
	for collection, indexes := range definitions {
		if _, err := s.db.Collection(collection).Indexes().CreateMany(ctx, indexes); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) LoadDecision(ctx context.Context, command kernel.KernelCommand) (kernel.Snapshot, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.Snapshot{}, err
	}
	return s.loadSnapshot(ctx, command.Target, command.Preconditions)
}

func (s *Store) loadSnapshot(ctx context.Context, target kernel.AggregateRef, preconditions []kernel.AggregatePrecondition) (kernel.Snapshot, error) {
	snapshot := kernel.Snapshot{}
	var aggregate aggregateDocument
	err := s.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(target)}}).Decode(&aggregate)
	if err == nil {
		snapshot.Exists = true
		snapshot.Revision = aggregate.Revision
		if len(aggregate.State) > 0 {
			var state kernel.AggregateState
			if err := decode(aggregate.State, &state); err != nil {
				return kernel.Snapshot{}, err
			}
			snapshot.State = &state
		}
	} else if !errors.Is(err, driver.ErrNoDocuments) {
		return kernel.Snapshot{}, err
	}

	snapshot.AcceptedEvents = make(map[kernel.UUIDv7]kernel.AcceptedEvent)
	if err := scan(ctx, s.db.Collection("events"), bson.D{}, func(document eventDocument) error {
		snapshot.AcceptedEvents[kernel.UUIDv7(document.ID)] = kernel.AcceptedEvent{EventType: document.EventType, Qualification: document.Qualification}
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.CurrentExecutions = make(map[kernel.ActorFQN]kernel.ExecutionTuple)
	if err := scan(ctx, s.db.Collection("executions"), bson.D{}, func(document executionDocument) error {
		snapshot.CurrentExecutions[kernel.ActorFQN(document.ID)] = kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7(document.ExecutionID), FencingEpoch: document.FencingEpoch}
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.Evidence = make(map[kernel.UUIDv7]kernel.EvidenceMetadata)
	if err := scan(ctx, s.db.Collection("evidence"), bson.D{}, func(document evidenceDocument) error {
		snapshot.Evidence[kernel.UUIDv7(document.ID)] = kernel.EvidenceMetadata{SHA256: kernel.Digest(document.SHA256), Available: document.Available}
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.Related = make(map[kernel.AggregateRef]kernel.RelatedSnapshot, len(preconditions))
	for _, precondition := range preconditions {
		var document aggregateDocument
		err := s.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(precondition.Aggregate)}}).Decode(&document)
		related := kernel.RelatedSnapshot{}
		if err == nil {
			related.Exists = true
			related.Revision = document.Revision
			if len(document.State) > 0 {
				var state kernel.AggregateState
				if err := decode(document.State, &state); err != nil {
					return kernel.Snapshot{}, err
				}
				related.State = &state
			}
		} else if !errors.Is(err, driver.ErrNoDocuments) {
			return kernel.Snapshot{}, err
		}
		snapshot.Related[precondition.Aggregate] = related
	}
	policy, err := s.loadPolicy(ctx)
	if err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.Authorization = policy
	snapshot.OpenReviews = make(map[kernel.CompletionReviewKey]kernel.AggregateRef)
	if err := scan(ctx, s.db.Collection("review_keys"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Key    kernel.CompletionReviewKey `json:"key"`
			Review kernel.AggregateRef        `json:"review"`
		}
		if err := decode(document.Data, &value); err != nil {
			return err
		}
		snapshot.OpenReviews[value.Key] = value.Review
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.Reviews = make(map[kernel.AggregateRef]kernel.CompletionReviewSnapshot)
	if err := scan(ctx, s.db.Collection("reviews"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Review   kernel.AggregateRef             `json:"review"`
			Progress kernel.CompletionReviewSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil {
			return err
		}
		snapshot.Reviews[value.Review] = value.Progress
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.EscalationKeys = make(map[kernel.EscalationKey]kernel.AggregateRef)
	if err := scan(ctx, s.db.Collection("escalation_keys"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Key        kernel.EscalationKey `json:"key"`
			Escalation kernel.AggregateRef  `json:"escalation"`
		}
		if err := decode(document.Data, &value); err != nil {
			return err
		}
		snapshot.EscalationKeys[value.Key] = value.Escalation
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.Escalations = make(map[kernel.AggregateRef]kernel.EscalationSnapshot)
	if err := scan(ctx, s.db.Collection("escalations"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Escalation kernel.AggregateRef       `json:"escalation"`
			Progress   kernel.EscalationSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil {
			return err
		}
		snapshot.Escalations[value.Escalation] = value.Progress
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.ReleasePlanKeys = make(map[kernel.ReleasePlanKey]kernel.AggregateRef)
	if err := scan(ctx, s.db.Collection("release_plan_keys"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Key         kernel.ReleasePlanKey `json:"key"`
			ReleasePlan kernel.AggregateRef   `json:"release_plan"`
		}
		if err := decode(document.Data, &value); err != nil {
			return err
		}
		if !value.Key.Valid() || value.ReleasePlan.Kind != kernel.AggregateReleasePlan || !value.ReleasePlan.ID.Valid() || document.ID != releasePlanKey(value.Key) {
			return ErrCorruptAggregate
		}
		snapshot.ReleasePlanKeys[value.Key] = value.ReleasePlan
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.ReleasePlans = make(map[kernel.AggregateRef]kernel.ReleasePlanSnapshot)
	if err := scan(ctx, s.db.Collection("release_plans"), bson.D{}, func(document valueDocument) error {
		var value struct {
			ReleasePlan kernel.AggregateRef        `json:"release_plan"`
			Progress    kernel.ReleasePlanSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil {
			return err
		}
		if value.ReleasePlan.Kind != kernel.AggregateReleasePlan || value.ReleasePlan.ID != value.Progress.ReleasePlanID || !value.Progress.Valid() || document.ID != aggregateKey(value.ReleasePlan) {
			return ErrCorruptAggregate
		}
		snapshot.ReleasePlans[value.ReleasePlan] = value.Progress
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	for key, reference := range snapshot.ReleasePlanKeys {
		progress, found := snapshot.ReleasePlans[reference]
		if !found || progress.Key() != key {
			return kernel.Snapshot{}, ErrCorruptAggregate
		}
	}
	for reference, progress := range snapshot.ReleasePlans {
		if snapshot.ReleasePlanKeys[progress.Key()] != reference {
			return kernel.Snapshot{}, ErrCorruptAggregate
		}
	}
	snapshot.AttemptBudgets = make(map[kernel.AttemptBudgetKey]kernel.AttemptBudgetSnapshot)
	if err := scan(ctx, s.db.Collection("attempt_budgets"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Key    kernel.AttemptBudgetKey      `json:"key"`
			Budget kernel.AttemptBudgetSnapshot `json:"budget"`
		}
		if err := decode(document.Data, &value); err != nil {
			return err
		}
		snapshot.AttemptBudgets[value.Key] = value.Budget
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.WorkProfiles = make(map[kernel.AggregateRef]kernel.WorkProfileSnapshot)
	if err := scan(ctx, s.db.Collection("work_profiles"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Task    kernel.AggregateRef        `json:"task"`
			Profile kernel.WorkProfileSnapshot `json:"profile"`
		}
		if err := decode(document.Data, &value); err != nil || value.Task.Kind != kernel.AggregateTask || !value.Profile.Valid() || value.Profile.Profile.TaskID != value.Task.ID || document.ID != aggregateKey(value.Task) {
			return ErrCorruptAggregate
		}
		snapshot.WorkProfiles[value.Task] = value.Profile
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.QualifiedAssignments = make(map[kernel.AggregateRef]kernel.QualifiedAssignmentAuthorization)
	if err := scan(ctx, s.db.Collection("qualified_assignments"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Task          kernel.AggregateRef                     `json:"task"`
			Authorization kernel.QualifiedAssignmentAuthorization `json:"authorization"`
		}
		if err := decode(document.Data, &value); err != nil || value.Task.Kind != kernel.AggregateTask || !value.Authorization.Valid() || value.Authorization.TaskID != value.Task.ID || document.ID != aggregateKey(value.Task) {
			return ErrCorruptAggregate
		}
		snapshot.QualifiedAssignments[value.Task] = value.Authorization
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.VariantGroupKeys = make(map[kernel.VariantGroupKey]kernel.AggregateRef)
	if err := scan(ctx, s.db.Collection("variant_group_keys"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Key   kernel.VariantGroupKey `json:"key"`
			Group kernel.AggregateRef    `json:"group"`
		}
		if err := decode(document.Data, &value); err != nil || !value.Key.Valid() || value.Group.Kind != kernel.AggregateVariantGroup || !value.Group.ID.Valid() || document.ID != variantGroupKey(value.Key) {
			return ErrCorruptAggregate
		}
		snapshot.VariantGroupKeys[value.Key] = value.Group
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	snapshot.VariantGroups = make(map[kernel.AggregateRef]kernel.VariantGroupSnapshot)
	if err := scan(ctx, s.db.Collection("variant_groups"), bson.D{}, func(document valueDocument) error {
		var value struct {
			Group    kernel.AggregateRef         `json:"group"`
			Progress kernel.VariantGroupSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil || value.Group.Kind != kernel.AggregateVariantGroup || value.Group.ID != value.Progress.VariantGroupID || !value.Progress.Valid() || document.ID != aggregateKey(value.Group) {
			return ErrCorruptAggregate
		}
		snapshot.VariantGroups[value.Group] = value.Progress
		return nil
	}); err != nil {
		return kernel.Snapshot{}, err
	}
	for key, reference := range snapshot.VariantGroupKeys {
		progress, found := snapshot.VariantGroups[reference]
		if !found || progress.Key() != key {
			return kernel.Snapshot{}, ErrCorruptAggregate
		}
	}
	for reference, progress := range snapshot.VariantGroups {
		if snapshot.VariantGroupKeys[progress.Key()] != reference {
			return kernel.Snapshot{}, ErrCorruptAggregate
		}
	}
	if err := s.loadPhase4State(ctx, &snapshot); err != nil {
		return kernel.Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Store) LookupReceipt(ctx context.Context, command kernel.KernelCommand, at time.Time) (kernel.CommandReceipt, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	fingerprint, err := kernel.CommandFingerprint(command)
	if err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	scope, err := kernel.IdempotencyScopeDigest(command)
	if err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	filter := bson.D{{Key: "_id", Value: string(command.CommandID)}}
	var document receiptDocument
	err = s.db.Collection("receipts").FindOne(ctx, filter).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		err = s.db.Collection("receipts").FindOne(ctx, bson.D{{Key: "scope", Value: string(scope)}}).Decode(&document)
	}
	if errors.Is(err, driver.ErrNoDocuments) {
		return kernel.CommandReceipt{}, false, nil
	}
	if err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	if document.Fingerprint != string(fingerprint) {
		if document.ID == string(command.CommandID) {
			return kernel.CommandReceipt{}, false, kernel.ErrCommandIdentityConflict
		}
		return kernel.CommandReceipt{}, false, kernel.ErrIdempotencyKeyConflict
	}
	snapshot, err := s.loadSnapshot(ctx, command.Target, nil)
	if err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	if !snapshot.Authorization.Authorize(command, snapshot.State, at).CanReadTarget {
		return kernel.CommandReceipt{}, false, kernel.ErrReceiptAccessDenied
	}
	var receipt kernel.CommandReceipt
	if err := decode(document.Data, &receipt); err != nil {
		return kernel.CommandReceipt{}, false, err
	}
	return receipt, true, nil
}

func (s *Store) VerifyAggregate(ctx context.Context, target kernel.AggregateRef) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if !target.Valid() {
		return ErrInvalidDecision
	}
	var aggregate aggregateDocument
	if err := s.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(target)}}).Decode(&aggregate); err != nil {
		return err
	}
	cursor, err := s.db.Collection("events").Find(ctx, bson.D{{Key: "aggregate_key", Value: aggregateKey(target)}}, options.Find().SetSort(bson.D{{Key: "revision", Value: 1}}))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	events := make([]kernel.DomainEvent, 0)
	for cursor.Next(ctx) {
		var document eventDocument
		if err := cursor.Decode(&document); err != nil {
			return err
		}
		var event kernel.DomainEvent
		if err := decode(document.Data, &event); err != nil {
			return err
		}
		events = append(events, event)
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	folded, err := kernel.FoldAggregate(events)
	if err != nil {
		return err
	}
	var stored *kernel.AggregateState
	if len(aggregate.State) > 0 {
		stored = new(kernel.AggregateState)
		if err := decode(aggregate.State, stored); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(folded, stored) {
		return ErrCorruptAggregate
	}
	return nil
}

func (s *Store) RecordIdentityConflict(ctx context.Context, audit kernel.IdentityConflictAudit) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if !audit.CommandID.Valid() || !audit.IdempotencyScope.Valid() || !audit.Fingerprint.Valid() || audit.ReasonCode == "" || audit.ObservedAt.IsZero() || !audit.ProvenanceDigest.Valid() {
		return ErrInvalidDecision
	}
	data, err := encode(audit)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	_, err = s.db.Collection("identity_conflicts").InsertOne(ctx, bson.D{
		{Key: "_id", Value: hex.EncodeToString(digest[:])}, {Key: "command_id", Value: string(audit.CommandID)}, {Key: "observed_at", Value: audit.ObservedAt}, {Key: "data", Value: data},
	})
	return err
}

func (s *Store) Commit(ctx context.Context, expected kernel.Snapshot, decision kernel.Decision) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if err := validateDecision(expected, decision); err != nil {
		return err
	}
	session, err := s.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if err := s.commitTransaction(transactionContext, expected, decision); err != nil {
			return nil, err
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err != nil {
		if driver.IsDuplicateKeyError(err) || errors.Is(err, ErrConflict) {
			return ErrConflict
		}
		if errors.Is(err, kernel.ErrDecisionAlreadyCommitted) || errors.Is(err, kernel.ErrIdempotencyKeyConflict) {
			return err
		}
		return err
	}
	s.faultMu.Lock()
	uncertain := s.uncertain
	s.uncertain = false
	s.faultMu.Unlock()
	if uncertain {
		return kernel.ErrCommitUncertain
	}
	return nil
}

func (s *Store) commitTransaction(ctx context.Context, expected kernel.Snapshot, decision kernel.Decision) error {
	var existing receiptDocument
	err := s.db.Collection("receipts").FindOne(ctx, bson.D{{Key: "_id", Value: string(decision.Receipt.CommandID)}}).Decode(&existing)
	if err == nil {
		if existing.Fingerprint == string(decision.CommandFingerprint) && existing.Scope == string(decision.IdempotencyScope) {
			return kernel.ErrDecisionAlreadyCommitted
		}
		return ErrConflict
	}
	if !errors.Is(err, driver.ErrNoDocuments) {
		return err
	}
	err = s.db.Collection("receipts").FindOne(ctx, bson.D{{Key: "scope", Value: string(decision.IdempotencyScope)}}).Decode(&existing)
	if err == nil {
		if existing.Fingerprint == string(decision.CommandFingerprint) {
			return kernel.ErrDecisionAlreadyCommitted
		}
		return kernel.ErrIdempotencyKeyConflict
	}
	if !errors.Is(err, driver.ErrNoDocuments) {
		return err
	}
	if err := s.checkGuards(ctx, expected, decision); err != nil {
		return err
	}
	if err := s.inject("before-state"); err != nil {
		return err
	}
	if decision.Receipt.ResultingRevision != nil {
		state, err := encodeOptional(decision.NextState)
		if err != nil {
			return err
		}
		document := aggregateDocument{ID: aggregateKey(decision.Receipt.Target), Revision: *decision.Receipt.ResultingRevision, State: state}
		if expected.Exists {
			result, err := s.db.Collection("aggregates").ReplaceOne(ctx, bson.D{{Key: "_id", Value: document.ID}, {Key: "revision", Value: expected.Revision}}, document)
			if err != nil {
				return err
			}
			if result.ModifiedCount != 1 {
				return ErrConflict
			}
		} else if _, err := s.db.Collection("aggregates").InsertOne(ctx, document); err != nil {
			return err
		}
	}
	if err := s.inject("before-events"); err != nil {
		return err
	}
	for _, event := range decision.Events {
		data, err := encode(event)
		if err != nil {
			return err
		}
		qualification, err := s.applyRegistryAndReview(ctx, event, decision.WorkBudget)
		if err != nil {
			return err
		}
		document := eventDocument{ID: string(event.EventID), AggregateKey: aggregateKey(event.Aggregate), Revision: event.AggregateRevision, EventType: event.EventType, Qualification: qualification, Data: data}
		if _, err := s.db.Collection("events").InsertOne(ctx, document); err != nil {
			return err
		}
	}
	if decision.AttemptBudget != nil {
		if err := s.applyAttemptBudget(ctx, *decision.AttemptBudget); err != nil {
			return err
		}
	}
	if err := s.inject("before-receipt"); err != nil {
		return err
	}
	receiptBytes, err := encode(decision.Receipt)
	if err != nil {
		return err
	}
	if _, err := s.db.Collection("receipts").InsertOne(ctx, receiptDocument{
		ID: string(decision.Receipt.CommandID), Scope: string(decision.IdempotencyScope), Fingerprint: string(decision.CommandFingerprint), TargetKey: aggregateKey(decision.Receipt.Target), Data: receiptBytes,
	}); err != nil {
		return err
	}
	if err := s.inject("before-authority"); err != nil {
		return err
	}
	if err := s.insertValue(ctx, "authority", string(decision.Receipt.CommandID), decision.Authority); err != nil {
		return err
	}
	provenanceDigest, _ := decision.Provenance.Digest()
	if err := s.insertValue(ctx, "provenance", string(provenanceDigest), decision.Provenance); err != nil {
		return err
	}
	if err := s.inject("before-outbox"); err != nil {
		return err
	}
	for _, intent := range decision.Outbox {
		data, err := encode(intent)
		if err != nil {
			return err
		}
		if _, err := s.db.Collection("outbox").InsertOne(ctx, bson.D{
			{Key: "_id", Value: string(intent.IntentID)}, {Key: "event_id", Value: string(intent.EventID)}, {Key: "kind", Value: intent.Kind}, {Key: "state", Value: "PENDING"}, {Key: "claim_epoch", Value: int64(0)}, {Key: "attempts", Value: int64(0)}, {Key: "data", Value: data},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) checkGuards(ctx context.Context, expected kernel.Snapshot, decision kernel.Decision) error {
	var aggregate aggregateDocument
	err := s.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(decision.Receipt.Target)}}).Decode(&aggregate)
	exists := err == nil
	if err != nil && !errors.Is(err, driver.ErrNoDocuments) {
		return err
	}
	if expected.Exists != exists || (exists && aggregate.Revision != expected.Revision) {
		return ErrConflict
	}
	for actor, tuple := range decision.Guards.Executions {
		var document executionDocument
		err := s.db.Collection("executions").FindOne(ctx, bson.D{{Key: "_id", Value: string(actor)}}).Decode(&document)
		if err != nil {
			if errors.Is(err, driver.ErrNoDocuments) {
				return ErrConflict
			}
			return err
		}
		if document.ExecutionID != string(tuple.ExecutionID) || document.FencingEpoch != tuple.FencingEpoch {
			return ErrConflict
		}
	}
	for _, actor := range decision.Guards.AbsentExecutions {
		err := s.db.Collection("executions").FindOne(ctx, bson.D{{Key: "_id", Value: string(actor)}}).Err()
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, driver.ErrNoDocuments) {
			return err
		}
	}
	for _, parent := range decision.Guards.ParentIDs {
		err := s.db.Collection("events").FindOne(ctx, bson.D{{Key: "_id", Value: string(parent)}}).Err()
		if err != nil {
			if errors.Is(err, driver.ErrNoDocuments) {
				return ErrConflict
			}
			return err
		}
	}
	for _, reference := range decision.Guards.EvidenceRefs {
		var document evidenceDocument
		err := s.db.Collection("evidence").FindOne(ctx, bson.D{{Key: "_id", Value: string(reference.EvidenceID)}}).Decode(&document)
		if err != nil {
			if errors.Is(err, driver.ErrNoDocuments) {
				return ErrConflict
			}
			return err
		}
		if !document.Available || document.SHA256 != string(reference.SHA256) {
			return ErrConflict
		}
	}
	for _, precondition := range decision.Guards.Preconditions {
		var document aggregateDocument
		err := s.db.Collection("aggregates").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(precondition.Aggregate)}}).Decode(&document)
		if precondition.Expected.MustNotExist {
			if err == nil || !errors.Is(err, driver.ErrNoDocuments) {
				return ErrConflict
			}
		} else if err != nil || document.Revision != precondition.Expected.Revision {
			return ErrConflict
		}
	}
	if decision.Guards.PolicyDigest.Valid() {
		policy, err := s.loadPolicy(ctx)
		if err != nil || policy.PolicyDigest != decision.Guards.PolicyDigest || policy.Revision != decision.Guards.PolicyRevision {
			return ErrConflict
		}
	}
	for _, key := range decision.Guards.AbsentReviewKeys {
		err := s.db.Collection("review_keys").FindOne(ctx, bson.D{{Key: "_id", Value: reviewKey(key)}}).Err()
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, driver.ErrNoDocuments) {
			return err
		}
	}
	for _, key := range decision.Guards.AbsentEscalationKeys {
		err := s.db.Collection("escalation_keys").FindOne(ctx, bson.D{{Key: "_id", Value: escalationKey(key)}}).Err()
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, driver.ErrNoDocuments) {
			return err
		}
	}
	for _, key := range decision.Guards.AbsentReleaseKeys {
		err := s.db.Collection("release_plan_keys").FindOne(ctx, bson.D{{Key: "_id", Value: releasePlanKey(key)}}).Err()
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, driver.ErrNoDocuments) {
			return err
		}
	}
	for _, key := range decision.Guards.AbsentVariantKeys {
		err := s.db.Collection("variant_group_keys").FindOne(ctx, bson.D{{Key: "_id", Value: variantGroupKey(key)}}).Err()
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, driver.ErrNoDocuments) {
			return err
		}
	}
	return nil
}

func (s *Store) applyRegistryAndReview(ctx context.Context, event kernel.DomainEvent, debit *kernel.WorkBudgetDebitDecision) (string, error) {
	if handled, err := s.applyPhase4Event(ctx, event, debit); handled {
		return "", err
	}
	switch event.EventType {
	case "tekroo.event.execution.registered":
		var payload struct {
			ActorFQN     kernel.ActorFQN `json:"actor_fqn"`
			ExecutionID  kernel.UUIDv7   `json:"execution_id"`
			FencingEpoch uint64          `json:"fencing_epoch"`
		}
		if err := decode(event.Payload, &payload); err != nil {
			return "", err
		}
		_, err := s.db.Collection("executions").InsertOne(ctx, executionDocument{ID: string(payload.ActorFQN), ExecutionID: string(payload.ExecutionID), FencingEpoch: payload.FencingEpoch})
		return "", err
	case "tekroo.event.execution.replaced":
		var payload struct {
			ActorFQN        kernel.ActorFQN `json:"actor_fqn"`
			NewExecutionID  kernel.UUIDv7   `json:"new_execution_id"`
			NewFencingEpoch uint64          `json:"new_fencing_epoch"`
		}
		if err := decode(event.Payload, &payload); err != nil {
			return "", err
		}
		result, err := s.db.Collection("executions").UpdateOne(ctx, bson.D{{Key: "_id", Value: string(payload.ActorFQN)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "execution_id", Value: string(payload.NewExecutionID)}, {Key: "fencing_epoch", Value: payload.NewFencingEpoch}}}})
		if err != nil || result.MatchedCount != 1 {
			return "", ErrConflict
		}
		return "", nil
	case "tekroo.event.evidence.registered":
		var payload struct {
			SHA256       kernel.Digest `json:"sha256"`
			Availability string        `json:"availability"`
		}
		if err := decode(event.Payload, &payload); err != nil {
			return "", err
		}
		_, err := s.db.Collection("evidence").InsertOne(ctx, evidenceDocument{ID: string(event.Aggregate.ID), SHA256: string(payload.SHA256), Available: payload.Availability == string(kernel.EvidenceAvailable)})
		return "", err
	case "tekroo.event.task.work-profile-bound":
		if event.Aggregate.Kind != kernel.AggregateTask {
			return "", ErrConflict
		}
		profile, err := kernel.WorkRiskProfileFromPayload(event.Payload)
		if err != nil || profile.TaskID != event.Aggregate.ID {
			return "", ErrConflict
		}
		progress := kernel.WorkProfileSnapshot{Profile: profile, BoundEventID: event.EventID, TaskRevision: event.AggregateRevision}
		if !progress.Valid() {
			return "", ErrConflict
		}
		data, err := encode(struct {
			Task    kernel.AggregateRef        `json:"task"`
			Profile kernel.WorkProfileSnapshot `json:"profile"`
		}{event.Aggregate, progress})
		if err != nil {
			return "", err
		}
		_, err = s.db.Collection("work_profiles").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data}, options.Replace().SetUpsert(true))
		return "", err
	case "tekroo.event.task.qualified-assignment-authorized":
		if event.Aggregate.Kind != kernel.AggregateTask {
			return "", ErrConflict
		}
		authorization, err := kernel.QualifiedAssignmentAuthorizationFromPayload(event.Payload, event.EventID)
		if err != nil || authorization.TaskID != event.Aggregate.ID || authorization.ExpectedTaskRevision+1 != event.AggregateRevision {
			return "", ErrConflict
		}
		data, err := encode(struct {
			Task          kernel.AggregateRef                     `json:"task"`
			Authorization kernel.QualifiedAssignmentAuthorization `json:"authorization"`
		}{event.Aggregate, authorization})
		if err != nil {
			return "", err
		}
		_, err = s.db.Collection("qualified_assignments").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data}, options.Replace().SetUpsert(true))
		return "", err
	case "tekroo.event.variant-group.opened":
		if event.Aggregate.Kind != kernel.AggregateVariantGroup || event.AggregateRevision != 1 {
			return "", ErrConflict
		}
		progress, err := kernel.VariantGroupFromOpenPayload(event.Payload, event.EventID)
		if err != nil || progress.VariantGroupID != event.Aggregate.ID {
			return "", ErrConflict
		}
		key := progress.Key()
		if err := s.insertValue(ctx, "variant_group_keys", variantGroupKey(key), struct {
			Key   kernel.VariantGroupKey `json:"key"`
			Group kernel.AggregateRef    `json:"group"`
		}{key, event.Aggregate}); err != nil {
			return "", err
		}
		return "", s.insertValue(ctx, "variant_groups", aggregateKey(event.Aggregate), struct {
			Group    kernel.AggregateRef         `json:"group"`
			Progress kernel.VariantGroupSnapshot `json:"progress"`
		}{event.Aggregate, progress})
	case "tekroo.event.variant-group.candidate-submitted", "tekroo.event.variant-group.comparison-recorded", "tekroo.event.variant-group.selected":
		var document valueDocument
		if err := s.db.Collection("variant_groups").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&document); err != nil {
			return "", err
		}
		var value struct {
			Group    kernel.AggregateRef         `json:"group"`
			Progress kernel.VariantGroupSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil || value.Group != event.Aggregate || value.Progress.Revision+1 != event.AggregateRevision {
			return "", ErrConflict
		}
		next, valid := kernel.ApplyVariantEvent(value.Progress, event)
		if !valid || next.Revision != event.AggregateRevision {
			return "", ErrConflict
		}
		value.Progress = next
		data, err := encode(value)
		if err != nil {
			return "", err
		}
		result, err := s.db.Collection("variant_groups").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data})
		if err != nil || result.ModifiedCount != 1 {
			return "", ErrConflict
		}
		return "", nil
	case "tekroo.event.completion-review.opened":
		key, err := kernel.CompletionReviewKeyFromPayload(event.Payload)
		if err != nil {
			return "", err
		}
		progress, err := kernel.CompletionReviewFromPayload(event.Payload)
		if err != nil {
			return "", err
		}
		progress.ReviewID = event.Aggregate.ID
		if err := s.insertValue(ctx, "review_keys", reviewKey(key), struct {
			Key    kernel.CompletionReviewKey `json:"key"`
			Review kernel.AggregateRef        `json:"review"`
		}{key, event.Aggregate}); err != nil {
			return "", err
		}
		return "", s.insertValue(ctx, "reviews", aggregateKey(event.Aggregate), struct {
			Review   kernel.AggregateRef             `json:"review"`
			Progress kernel.CompletionReviewSnapshot `json:"progress"`
		}{event.Aggregate, progress})
	case "tekroo.event.completion-review.result-recorded":
		reviewID, policyRevision, result, err := kernel.ReviewBranchResultFromPayload(event.Payload)
		if err != nil || reviewID != event.Aggregate.ID {
			return "", ErrConflict
		}
		var document valueDocument
		if err := s.db.Collection("reviews").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&document); err != nil {
			return "", err
		}
		var value struct {
			Review   kernel.AggregateRef             `json:"review"`
			Progress kernel.CompletionReviewSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil || value.Progress.BranchPolicyRevision != policyRevision {
			return "", ErrConflict
		}
		result.Authority = event.Authority
		result.EventID = event.EventID
		result.DecidedAt = event.CommittedAt
		next, valid := kernel.ApplyReviewBranchResult(value.Progress, result)
		if !valid {
			return "", ErrConflict
		}
		value.Progress = next
		data, _ := encode(value)
		if _, err := s.db.Collection("reviews").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data}); err != nil {
			return "", err
		}
		return next.Join.Status, nil
	case "tekroo.event.completion-review.finalized":
		var document valueDocument
		if err := s.db.Collection("reviews").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&document); err != nil {
			return "", err
		}
		var value struct {
			Review   kernel.AggregateRef             `json:"review"`
			Progress kernel.CompletionReviewSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil {
			return "", err
		}
		next, valid := kernel.ApplyReviewFinalization(value.Progress, event.EventID, event.Payload)
		if !valid {
			return "", ErrConflict
		}
		value.Progress = next
		data, _ := encode(value)
		if _, err := s.db.Collection("reviews").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data}); err != nil {
			return "", err
		}
		return next.Join.Status, nil
	case "tekroo.event.escalation.opened":
		if event.Aggregate.Kind != kernel.AggregateEscalation || event.AggregateRevision != 1 {
			return "", ErrConflict
		}
		progress, err := kernel.EscalationFromOpenPayload(event.Payload)
		if err != nil || progress.EscalationID != event.Aggregate.ID {
			return "", ErrConflict
		}
		progress.OpeningEventID = event.EventID
		progress.Revision = event.AggregateRevision
		if !progress.Valid() {
			return "", ErrConflict
		}
		key := progress.Key()
		if err := s.insertValue(ctx, "escalation_keys", escalationKey(key), struct {
			Key        kernel.EscalationKey `json:"key"`
			Escalation kernel.AggregateRef  `json:"escalation"`
		}{key, event.Aggregate}); err != nil {
			return "", err
		}
		return "", s.insertValue(ctx, "escalations", aggregateKey(event.Aggregate), struct {
			Escalation kernel.AggregateRef       `json:"escalation"`
			Progress   kernel.EscalationSnapshot `json:"progress"`
		}{event.Aggregate, progress})
	case "tekroo.event.escalation.resolved":
		var document valueDocument
		if err := s.db.Collection("escalations").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&document); err != nil {
			return "", err
		}
		var value struct {
			Escalation kernel.AggregateRef       `json:"escalation"`
			Progress   kernel.EscalationSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil || value.Progress.Revision+1 != event.AggregateRevision {
			return "", ErrConflict
		}
		next, valid := kernel.ApplyEscalationResolution(value.Progress, event.Authority, event.EventID, event.CommittedAt, event.Payload)
		if !valid || next.Revision != event.AggregateRevision {
			return "", ErrConflict
		}
		value.Progress = next
		data, _ := encode(value)
		result, err := s.db.Collection("escalations").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data})
		if err != nil || result.ModifiedCount != 1 {
			return "", ErrConflict
		}
		return "", nil
	case "tekroo.event.release-plan.created":
		if event.Aggregate.Kind != kernel.AggregateReleasePlan || event.AggregateRevision != 1 {
			return "", ErrConflict
		}
		progress, err := kernel.ReleasePlanFromCreatePayload(event.Payload, event.EventID)
		if err != nil || progress.ReleasePlanID != event.Aggregate.ID || progress.Revision != event.AggregateRevision {
			return "", ErrConflict
		}
		key := progress.Key()
		if err := s.insertValue(ctx, "release_plan_keys", releasePlanKey(key), struct {
			Key         kernel.ReleasePlanKey `json:"key"`
			ReleasePlan kernel.AggregateRef   `json:"release_plan"`
		}{key, event.Aggregate}); err != nil {
			return "", err
		}
		return "", s.insertValue(ctx, "release_plans", aggregateKey(event.Aggregate), struct {
			ReleasePlan kernel.AggregateRef        `json:"release_plan"`
			Progress    kernel.ReleasePlanSnapshot `json:"progress"`
		}{event.Aggregate, progress})
	case "tekroo.event.release-plan.qualification-recorded", "tekroo.event.release-plan.execution-requested", "tekroo.event.release-plan.result-recorded", "tekroo.event.release-plan.reconciliation-recorded", "tekroo.event.release-plan.finalized":
		var document valueDocument
		if err := s.db.Collection("release_plans").FindOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}).Decode(&document); err != nil {
			return "", err
		}
		var value struct {
			ReleasePlan kernel.AggregateRef        `json:"release_plan"`
			Progress    kernel.ReleasePlanSnapshot `json:"progress"`
		}
		if err := decode(document.Data, &value); err != nil || value.ReleasePlan != event.Aggregate || value.Progress.Revision+1 != event.AggregateRevision {
			return "", ErrConflict
		}
		next, valid := kernel.ApplyReleaseEvent(value.Progress, event)
		if !valid || next.Revision != event.AggregateRevision {
			return "", ErrConflict
		}
		value.Progress = next
		data, err := encode(value)
		if err != nil {
			return "", err
		}
		result, err := s.db.Collection("release_plans").ReplaceOne(ctx, bson.D{{Key: "_id", Value: aggregateKey(event.Aggregate)}}, valueDocument{ID: aggregateKey(event.Aggregate), Data: data})
		if err != nil || result.ModifiedCount != 1 {
			return "", ErrConflict
		}
		return "", nil
	default:
		return "", nil
	}
}

func (s *Store) applyAttemptBudget(ctx context.Context, decision kernel.AttemptBudgetDecision) error {
	key := attemptBudgetKey(decision.Key)
	current := kernel.AttemptBudgetSnapshot{Limit: decision.Limit, Attempts: make(map[string]kernel.Digest)}
	var document valueDocument
	err := s.db.Collection("attempt_budgets").FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&document)
	if err == nil {
		var value struct {
			Key    kernel.AttemptBudgetKey      `json:"key"`
			Budget kernel.AttemptBudgetSnapshot `json:"budget"`
		}
		if err := decode(document.Data, &value); err != nil {
			return err
		}
		current = value.Budget
	} else if !errors.Is(err, driver.ErrNoDocuments) {
		return err
	}
	if current.Used != decision.ExpectedUsed || current.Limit != decision.Limit {
		return ErrConflict
	}
	if prior, exists := current.Attempts[decision.AttemptKey]; exists && prior != decision.ConditionDigest {
		return ErrConflict
	}
	current.Attempts[decision.AttemptKey] = decision.ConditionDigest
	current.Used++
	data, _ := encode(struct {
		Key    kernel.AttemptBudgetKey      `json:"key"`
		Budget kernel.AttemptBudgetSnapshot `json:"budget"`
	}{decision.Key, current})
	_, err = s.db.Collection("attempt_budgets").ReplaceOne(ctx, bson.D{{Key: "_id", Value: key}}, valueDocument{ID: key, Data: data}, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) loadPolicy(ctx context.Context) (kernel.AuthorizationPolicy, error) {
	var document metadataDocument
	if err := s.db.Collection("metadata").FindOne(ctx, bson.D{{Key: "_id", Value: "authorization"}}).Decode(&document); err != nil {
		return kernel.AuthorizationPolicy{}, err
	}
	var policy kernel.AuthorizationPolicy
	if err := decode(document.Data, &policy); err != nil {
		return kernel.AuthorizationPolicy{}, err
	}
	return normalizePolicy(policy), nil
}

func normalizePolicy(policy kernel.AuthorizationPolicy) kernel.AuthorizationPolicy {
	if policy.Requirements.AttemptLimits == nil {
		policy.Requirements.AttemptLimits = make(map[string]uint32)
	}
	return policy
}

func (s *Store) insertValue(ctx context.Context, collection, id string, value any) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	_, err = s.db.Collection(collection).InsertOne(ctx, valueDocument{ID: id, Data: data})
	return err
}

func (s *Store) inject(point string) error {
	s.faultMu.Lock()
	defer s.faultMu.Unlock()
	if s.fault == point {
		return fmt.Errorf("%w: %s", ErrInjectedFault, point)
	}
	return nil
}

func validateDecision(expected kernel.Snapshot, decision kernel.Decision) error {
	if !decision.CommandFingerprint.Valid() || !decision.IdempotencyScope.Valid() || !decision.Receipt.CommandID.Valid() || !decision.Receipt.Target.Valid() || !decision.Authority.Principal.Valid() {
		return ErrInvalidDecision
	}
	if decision.WorkBudget != nil && (!decision.WorkBudget.Valid() || decision.Receipt.CommandType != "tekroo.command.work-invocation.authorize") {
		return ErrInvalidDecision
	}
	provenanceDigest, err := decision.Provenance.Digest()
	if err != nil || !decision.Provenance.Valid() || provenanceDigest != decision.Receipt.ProvenanceDigest || decision.Provenance.CommandID != decision.Receipt.CommandID || decision.Provenance.CommandFingerprint != decision.CommandFingerprint {
		return ErrInvalidDecision
	}
	if decision.Authority.PolicyDigest.Valid() && (decision.Authority.PolicyDigest != decision.Provenance.PolicyDigest || decision.Authority.PolicyRevision != decision.Provenance.PolicyRevision || !reflect.DeepEqual(decision.Authority.GrantDigests, decision.Provenance.GrantDigests) || !reflect.DeepEqual(decision.Authority.DelegationDigests, decision.Provenance.DelegationDigests)) {
		return ErrInvalidDecision
	}
	if len(decision.Events) == 0 {
		if decision.NextState != nil || decision.Receipt.ResultingRevision != nil || len(decision.Receipt.EventIDs) != 0 || len(decision.Outbox) != 0 || decision.Receipt.OutcomeCode == kernel.OutcomeApplied {
			return ErrInvalidDecision
		}
		return nil
	}
	if len(decision.Events) != len(decision.Receipt.EventIDs) || len(decision.Outbox) != len(decision.Events) {
		return ErrInvalidDecision
	}
	for index, event := range decision.Events {
		if !event.EventID.Valid() || event.Aggregate != decision.Receipt.Target || event.AggregateRevision != expected.Revision+uint64(index)+1 || event.LifecycleEpoch == 0 || event.EventID != decision.Receipt.EventIDs[index] || event.ProvenanceDigest != provenanceDigest {
			return ErrInvalidDecision
		}
		if decision.Outbox[index].EventID != event.EventID || !decision.Outbox[index].IntentID.Valid() || decision.Outbox[index].Kind == "" {
			return ErrInvalidDecision
		}
	}
	if decision.NextState != nil && (decision.Receipt.ResultingRevision == nil || decision.NextState.Revision != *decision.Receipt.ResultingRevision) {
		return ErrInvalidDecision
	}
	return nil
}

func scan[T any](ctx context.Context, collection *driver.Collection, filter bson.D, consume func(T) error) error {
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var value T
		if err := cursor.Decode(&value); err != nil {
			return err
		}
		if err := consume(value); err != nil {
			return err
		}
	}
	return cursor.Err()
}

func aggregateKey(reference kernel.AggregateRef) string {
	return string(reference.Kind) + ":" + string(reference.ID)
}

func reviewKey(key kernel.CompletionReviewKey) string {
	data, _ := encode(key)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func escalationKey(key kernel.EscalationKey) string {
	data, _ := encode(key)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func releasePlanKey(key kernel.ReleasePlanKey) string {
	data, _ := encode(key)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func variantGroupKey(key kernel.VariantGroupKey) string {
	data, _ := encode(key)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func attemptBudgetKey(key kernel.AttemptBudgetKey) string {
	return aggregateKey(key.Subject) + ":" + key.Operation
}

func encode(value any) ([]byte, error) { return json.Marshal(value) }

func encodeOptional(value any) ([]byte, error) {
	if value == nil || reflect.ValueOf(value).IsNil() {
		return nil, nil
	}
	return encode(value)
}

func decode(data []byte, target any) error { return json.Unmarshal(data, target) }

func requireDeadline(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		return ErrDeadlineRequired
	}
	return ctx.Err()
}
