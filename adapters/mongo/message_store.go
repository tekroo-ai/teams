package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type organizationalMessageDocument struct {
	ID               string                            `bson:"_id"`
	Recipient        string                            `bson:"recipient"`
	ThreadID         string                            `bson:"thread_id"`
	StepID           string                            `bson:"step_id"`
	Hop              uint32                            `bson:"hop"`
	CreatedAt        time.Time                         `bson:"created_at"`
	ExpiresAt        time.Time                         `bson:"expires_at"`
	State            organization.MessageDeliveryState `bson:"state"`
	Holder           string                            `bson:"holder,omitempty"`
	ExecutionID      string                            `bson:"execution_id,omitempty"`
	FencingEpoch     uint64                            `bson:"fencing_epoch,omitempty"`
	ClaimEpoch       uint64                            `bson:"claim_epoch"`
	LeaseUntil       time.Time                         `bson:"lease_until,omitempty"`
	Attempts         uint32                            `bson:"attempts"`
	Resolution       string                            `bson:"resolution,omitempty"`
	EvidenceDigest   string                            `bson:"evidence_digest,omitempty"`
	DeadLetterReason string                            `bson:"dead_letter_reason,omitempty"`
	ReaddressCount   uint32                            `bson:"readdress_count"`
	Data             []byte                            `bson:"data"`
}

type organizationalThreadDocument struct {
	ID              string   `bson:"_id"`
	Revision        uint64   `bson:"revision"`
	LastMessageID   string   `bson:"last_message_id"`
	LastStepID      string   `bson:"last_step_id"`
	LastHop         uint32   `bson:"last_hop"`
	MaximumHops     uint32   `bson:"maximum_hops"`
	BudgetAccountID string   `bson:"budget_account_id"`
	LifecycleEpoch  uint64   `bson:"lifecycle_epoch"`
	ScopeRevision   uint64   `bson:"scope_revision"`
	VisitedSteps    []string `bson:"visited_steps"`
	ProgressDigests []string `bson:"progress_digests"`
	LastRecipient   string   `bson:"last_recipient"`
	VisitedRoles    []string `bson:"visited_roles"`
}

func (s *Store) AppendMessage(ctx context.Context, message organization.OrganizationalMessage) error {
	return s.AppendMessages(ctx, []organization.OrganizationalMessage{message})
}

func (s *Store) AppendMessages(ctx context.Context, messages []organization.OrganizationalMessage) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if s == nil || s.client == nil || len(messages) == 0 || len(messages) > 64 {
		return organization.ErrInvalidOrganizationalMessage
	}
	for _, message := range messages {
		if message.Validate() != nil {
			return organization.ErrInvalidOrganizationalMessage
		}
	}
	session, err := s.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		for _, message := range messages {
			raw, marshalErr := json.Marshal(message)
			if marshalErr != nil {
				return nil, marshalErr
			}
			if advanceErr := s.advanceMessageThread(transactionContext, message); advanceErr != nil {
				return nil, advanceErr
			}
			document := organizationalMessageDocument{
				ID: string(message.ID), Recipient: string(message.Recipient), ThreadID: string(message.Flow.ThreadID), StepID: string(message.Flow.StepID), Hop: message.Flow.Hop,
				CreatedAt: message.CreatedAt, ExpiresAt: message.ExpiresAt, State: organization.MessagePending, Data: raw,
			}
			if _, insertErr := s.db.Collection("organizational_messages").InsertOne(transactionContext, document); insertErr != nil {
				return nil, insertErr
			}
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if driver.IsDuplicateKeyError(err) {
		return organization.ErrOrganizationalMessageConflict
	}
	return err
}

func (s *Store) advanceMessageThread(ctx context.Context, message organization.OrganizationalMessage) error {
	collection := s.db.Collection("organizational_message_threads")
	var current organizationalThreadDocument
	err := collection.FindOne(ctx, bson.D{{Key: "_id", Value: string(message.Flow.ThreadID)}}).Decode(&current)
	if errors.Is(err, driver.ErrNoDocuments) {
		if message.Flow.Hop != 1 {
			return organization.ErrOrganizationalLoop
		}
		_, err = collection.InsertOne(ctx, organizationalThreadDocument{
			ID: string(message.Flow.ThreadID), Revision: 1, LastMessageID: string(message.ID), LastStepID: string(message.Flow.StepID), LastHop: 1,
			MaximumHops: message.Flow.MaximumHops, BudgetAccountID: string(message.Flow.BudgetAccountID), LifecycleEpoch: message.Flow.LifecycleEpoch,
			ScopeRevision: message.Flow.ScopeRevision, VisitedSteps: []string{string(message.Flow.StepID)}, ProgressDigests: []string{string(message.Flow.ProgressDigest)},
			LastRecipient: string(message.Recipient), VisitedRoles: []string{messageActorRole(message.Sender), messageActorRole(message.Recipient)},
		})
		return err
	}
	if err != nil {
		return err
	}
	if message.Flow.Hop != current.LastHop+1 || message.Flow.MaximumHops != current.MaximumHops || string(message.Flow.BudgetAccountID) != current.BudgetAccountID || message.Flow.LifecycleEpoch != current.LifecycleEpoch || message.Flow.ScopeRevision != current.ScopeRevision || message.CausationID == nil || string(*message.CausationID) != current.LastMessageID || message.Flow.ParentStepID == nil || string(*message.Flow.ParentStepID) != current.LastStepID || string(message.Sender) != current.LastRecipient || containsString(current.VisitedSteps, string(message.Flow.StepID)) || containsString(current.ProgressDigests, string(message.Flow.ProgressDigest)) || containsString(current.VisitedRoles, messageActorRole(message.Recipient)) {
		return organization.ErrOrganizationalLoop
	}
	next := current
	next.Revision++
	next.LastMessageID = string(message.ID)
	next.LastStepID = string(message.Flow.StepID)
	next.LastHop = message.Flow.Hop
	next.VisitedSteps = append(next.VisitedSteps, string(message.Flow.StepID))
	next.ProgressDigests = append(next.ProgressDigests, string(message.Flow.ProgressDigest))
	next.LastRecipient = string(message.Recipient)
	next.VisitedRoles = append(next.VisitedRoles, messageActorRole(message.Recipient))
	result, err := collection.ReplaceOne(ctx, bson.D{{Key: "_id", Value: current.ID}, {Key: "revision", Value: current.Revision}}, next)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return organization.ErrOrganizationalMessageConflict
	}
	return nil
}

func (s *Store) AcquireMessage(ctx context.Context, recipient kernel.ActorFQN, execution kernel.ExecutionTuple, now time.Time, lease time.Duration, maximumAttempts uint32) (organization.MessageClaim, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.MessageClaim{}, err
	}
	if !recipient.Valid() || !execution.Valid() || now.IsZero() || lease <= 0 || maximumAttempts == 0 {
		return organization.MessageClaim{}, organization.ErrInvalidOrganizationalMessage
	}
	session, err := s.client.StartSession()
	if err != nil {
		return organization.MessageClaim{}, err
	}
	defer session.EndSession(ctx)
	var claimed organization.MessageClaim
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		role, found, loadErr := s.LoadRole(transactionContext, recipient)
		if loadErr != nil || !found || role.Execution != execution || role.Status != organization.RoleIdle {
			return nil, errors.Join(organization.ErrStaleOrganizationalClaim, loadErr)
		}
		filter := bson.D{
			{Key: "recipient", Value: string(recipient)}, {Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now}}}, {Key: "attempts", Value: bson.D{{Key: "$lt", Value: maximumAttempts}}},
			{Key: "$or", Value: bson.A{bson.D{{Key: "state", Value: organization.MessagePending}}, bson.D{{Key: "state", Value: organization.MessageClaimed}, {Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}}}},
		}
		update := bson.D{
			{Key: "$set", Value: bson.D{{Key: "state", Value: organization.MessageClaimed}, {Key: "holder", Value: string(recipient)}, {Key: "execution_id", Value: string(execution.ExecutionID)}, {Key: "fencing_epoch", Value: execution.FencingEpoch}, {Key: "lease_until", Value: now.Add(lease)}}},
			{Key: "$inc", Value: bson.D{{Key: "claim_epoch", Value: 1}, {Key: "attempts", Value: 1}}},
			{Key: "$unset", Value: bson.D{{Key: "resolution", Value: ""}, {Key: "evidence_digest", Value: ""}, {Key: "dead_letter_reason", Value: ""}}},
		}
		var document organizationalMessageDocument
		findErr := s.db.Collection("organizational_messages").FindOneAndUpdate(transactionContext, filter, update, options.FindOneAndUpdate().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetReturnDocument(options.After)).Decode(&document)
		if errors.Is(findErr, driver.ErrNoDocuments) {
			return nil, organization.ErrOrganizationalMessageNotFound
		}
		if findErr != nil {
			return nil, findErr
		}
		claimed, findErr = decodeOrganizationalClaim(document)
		return nil, findErr
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	return claimed, err
}

func (s *Store) RenewMessage(ctx context.Context, id kernel.UUIDv7, holder kernel.ActorFQN, execution kernel.ExecutionTuple, epoch uint64, now time.Time, lease time.Duration) error {
	if lease <= 0 {
		return organization.ErrInvalidOrganizationalMessage
	}
	return s.updateOrganizationalClaim(ctx, id, holder, execution, epoch, now, bson.D{{Key: "$set", Value: bson.D{{Key: "lease_until", Value: now.Add(lease)}}}})
}

func (s *Store) ResolveMessage(ctx context.Context, id kernel.UUIDv7, holder kernel.ActorFQN, execution kernel.ExecutionTuple, epoch uint64, now time.Time, resolution string, evidence kernel.Digest) error {
	if resolution == "" || !evidence.Valid() {
		return organization.ErrInvalidOrganizationalMessage
	}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: organization.MessageResolved}, {Key: "resolution", Value: resolution}, {Key: "evidence_digest", Value: string(evidence)}}}, {Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "execution_id", Value: ""}, {Key: "fencing_epoch", Value: ""}, {Key: "lease_until", Value: ""}}}}
	return s.updateOrganizationalClaim(ctx, id, holder, execution, epoch, now, update)
}

func (s *Store) YieldMessage(ctx context.Context, id kernel.UUIDv7, holder kernel.ActorFQN, execution kernel.ExecutionTuple, epoch uint64, now time.Time) error {
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: organization.MessagePending}}}, {Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "execution_id", Value: ""}, {Key: "fencing_epoch", Value: ""}, {Key: "lease_until", Value: ""}}}}
	return s.updateOrganizationalClaim(ctx, id, holder, execution, epoch, now, update)
}

func (s *Store) ReaddressMessage(ctx context.Context, id kernel.UUIDv7, sender kernel.ActorFQN, execution kernel.ExecutionTuple, recipient kernel.ActorFQN, now time.Time, maximumReaddresses uint32) (organization.OrganizationalMessage, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.OrganizationalMessage{}, err
	}
	if !id.Valid() || !sender.Valid() || !execution.Valid() || !recipient.Valid() || now.IsZero() || maximumReaddresses == 0 || maximumReaddresses > 8 {
		return organization.OrganizationalMessage{}, organization.ErrInvalidOrganizationalMessage
	}
	session, err := s.client.StartSession()
	if err != nil {
		return organization.OrganizationalMessage{}, err
	}
	defer session.EndSession(ctx)
	var updated organization.OrganizationalMessage
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		var document organizationalMessageDocument
		if findErr := s.db.Collection("organizational_messages").FindOne(transactionContext, bson.D{{Key: "_id", Value: string(id)}}).Decode(&document); findErr != nil {
			if errors.Is(findErr, driver.ErrNoDocuments) {
				return nil, organization.ErrOrganizationalMessageNotFound
			}
			return nil, findErr
		}
		claim, decodeErr := decodeOrganizationalClaim(document)
		if decodeErr != nil {
			return nil, decodeErr
		}
		message := claim.Message
		if document.State != organization.MessagePending || message.Sender != sender || message.SenderExecution != execution || message.Recipient == recipient || document.ReaddressCount >= maximumReaddresses || messageActorRole(message.Recipient) == messageActorRole(recipient) {
			return nil, organization.ErrOrganizationalMessageConflict
		}
		var thread organizationalThreadDocument
		if threadErr := s.db.Collection("organizational_message_threads").FindOne(transactionContext, bson.D{{Key: "_id", Value: string(message.Flow.ThreadID)}}).Decode(&thread); threadErr != nil {
			return nil, threadErr
		}
		if thread.LastMessageID != string(id) || containsString(thread.VisitedRoles, messageActorRole(recipient)) {
			return nil, organization.ErrOrganizationalLoop
		}
		prior := message.Recipient
		message.Recipient = recipient
		message.ReaddressHistory = append(message.ReaddressHistory, organization.MessageReaddress{From: prior, To: recipient, By: sender, At: now})
		if message.Validate() != nil {
			return nil, organization.ErrInvalidOrganizationalMessage
		}
		threadNext := thread
		threadNext.Revision++
		threadNext.LastRecipient = string(recipient)
		threadNext.VisitedRoles = replaceString(threadNext.VisitedRoles, messageActorRole(prior), messageActorRole(recipient))
		threadResult, replaceErr := s.db.Collection("organizational_message_threads").ReplaceOne(transactionContext, bson.D{{Key: "_id", Value: thread.ID}, {Key: "revision", Value: thread.Revision}}, threadNext)
		if replaceErr != nil || threadResult.MatchedCount != 1 {
			return nil, errors.Join(organization.ErrOrganizationalMessageConflict, replaceErr)
		}
		raw, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			return nil, marshalErr
		}
		messageResult, updateErr := s.db.Collection("organizational_messages").UpdateOne(transactionContext, bson.D{{Key: "_id", Value: string(id)}, {Key: "state", Value: organization.MessagePending}, {Key: "readdress_count", Value: document.ReaddressCount}}, bson.D{{Key: "$set", Value: bson.D{{Key: "recipient", Value: string(recipient)}, {Key: "data", Value: raw}}}, {Key: "$inc", Value: bson.D{{Key: "readdress_count", Value: 1}}}})
		if updateErr != nil || messageResult.MatchedCount != 1 {
			return nil, errors.Join(organization.ErrOrganizationalMessageConflict, updateErr)
		}
		updated = message
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	return updated, err
}

func (s *Store) updateOrganizationalClaim(ctx context.Context, id kernel.UUIDv7, holder kernel.ActorFQN, execution kernel.ExecutionTuple, epoch uint64, now time.Time, update bson.D) error {
	if err := requireDeadline(ctx); err != nil {
		return err
	}
	if !id.Valid() || !holder.Valid() || !execution.Valid() || epoch == 0 || now.IsZero() {
		return organization.ErrInvalidOrganizationalMessage
	}
	filter := bson.D{{Key: "_id", Value: string(id)}, {Key: "state", Value: organization.MessageClaimed}, {Key: "holder", Value: string(holder)}, {Key: "execution_id", Value: string(execution.ExecutionID)}, {Key: "fencing_epoch", Value: execution.FencingEpoch}, {Key: "claim_epoch", Value: epoch}, {Key: "lease_until", Value: bson.D{{Key: "$gt", Value: now}}}}
	result, err := s.db.Collection("organizational_messages").UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return organization.ErrStaleOrganizationalClaim
	}
	return nil
}

func (s *Store) SweepMessages(ctx context.Context, now time.Time, maximumAttempts uint32) (int64, int64, error) {
	if err := requireDeadline(ctx); err != nil {
		return 0, 0, err
	}
	if now.IsZero() || maximumAttempts == 0 {
		return 0, 0, organization.ErrInvalidOrganizationalMessage
	}
	var released, dead int64
	collection := s.db.Collection("organizational_messages")
	deadResult, err := collection.UpdateMany(ctx, bson.D{{Key: "state", Value: organization.MessageClaimed}, {Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}, {Key: "attempts", Value: bson.D{{Key: "$gte", Value: maximumAttempts}}}}, bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: organization.MessageDeadLetter}, {Key: "dead_letter_reason", Value: "MAX_ATTEMPTS_EXHAUSTED"}}}, {Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "execution_id", Value: ""}, {Key: "fencing_epoch", Value: ""}, {Key: "lease_until", Value: ""}}}})
	if err != nil {
		return 0, 0, err
	}
	dead = deadResult.ModifiedCount
	releasedResult, err := collection.UpdateMany(ctx, bson.D{{Key: "state", Value: organization.MessageClaimed}, {Key: "lease_until", Value: bson.D{{Key: "$lte", Value: now}}}, {Key: "attempts", Value: bson.D{{Key: "$lt", Value: maximumAttempts}}}}, bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: organization.MessagePending}}}, {Key: "$unset", Value: bson.D{{Key: "holder", Value: ""}, {Key: "execution_id", Value: ""}, {Key: "fencing_epoch", Value: ""}, {Key: "lease_until", Value: ""}}}})
	if err != nil {
		return 0, dead, err
	}
	released = releasedResult.ModifiedCount
	return released, dead, nil
}

func (s *Store) ReadMessage(ctx context.Context, id kernel.UUIDv7) (organization.MessageClaim, bool, error) {
	if err := requireDeadline(ctx); err != nil {
		return organization.MessageClaim{}, false, err
	}
	if !id.Valid() {
		return organization.MessageClaim{}, false, organization.ErrInvalidOrganizationalMessage
	}
	var document organizationalMessageDocument
	err := s.db.Collection("organizational_messages").FindOne(ctx, bson.D{{Key: "_id", Value: string(id)}}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return organization.MessageClaim{}, false, nil
	}
	if err != nil {
		return organization.MessageClaim{}, false, err
	}
	claim, err := decodeOrganizationalClaim(document)
	return claim, err == nil, err
}

func (s *Store) TraceMessageThread(ctx context.Context, thread kernel.UUIDv7) ([]organization.MessageClaim, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if !thread.Valid() {
		return nil, organization.ErrInvalidOrganizationalMessage
	}
	cursor, err := s.db.Collection("organizational_messages").Find(ctx, bson.D{{Key: "thread_id", Value: string(thread)}}, options.Find().SetSort(bson.D{{Key: "hop", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]organization.MessageClaim, 0)
	for cursor.Next(ctx) {
		var document organizationalMessageDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		claim, err := decodeOrganizationalClaim(document)
		if err != nil {
			return nil, err
		}
		result = append(result, claim)
	}
	return result, cursor.Err()
}

func (s *Store) ListDeadLetters(ctx context.Context, recipient kernel.ActorFQN, limit int64) ([]organization.MessageClaim, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if recipient != "" && !recipient.Valid() || limit <= 0 || limit > 1000 {
		return nil, organization.ErrInvalidOrganizationalMessage
	}
	filter := bson.D{{Key: "state", Value: organization.MessageDeadLetter}}
	if recipient != "" {
		filter = append(filter, bson.E{Key: "recipient", Value: string(recipient)})
	}
	cursor, err := s.db.Collection("organizational_messages").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := make([]organization.MessageClaim, 0)
	for cursor.Next(ctx) {
		var document organizationalMessageDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		claim, err := decodeOrganizationalClaim(document)
		if err != nil {
			return nil, err
		}
		result = append(result, claim)
	}
	return result, cursor.Err()
}

func decodeOrganizationalClaim(document organizationalMessageDocument) (organization.MessageClaim, error) {
	var message organization.OrganizationalMessage
	if err := json.Unmarshal(document.Data, &message); err != nil || message.Validate() != nil || string(message.ID) != document.ID || string(message.Recipient) != document.Recipient || string(message.Flow.ThreadID) != document.ThreadID || string(message.Flow.StepID) != document.StepID || message.Flow.Hop != document.Hop {
		return organization.MessageClaim{}, organization.ErrOrganizationalMessageConflict
	}
	claim := organization.MessageClaim{
		Message: message, State: document.State, Holder: kernel.ActorFQN(document.Holder),
		Execution:  kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7(document.ExecutionID), FencingEpoch: document.FencingEpoch},
		ClaimEpoch: document.ClaimEpoch, LeaseUntil: document.LeaseUntil, Attempts: document.Attempts,
		Resolution: document.Resolution, Evidence: kernel.Digest(document.EvidenceDigest), DeadReason: document.DeadLetterReason,
	}
	return claim, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func messageActorRole(actor kernel.ActorFQN) string {
	text := string(actor)
	separator := strings.Index(text, "::")
	instance := strings.LastIndex(text, "-")
	if separator < 0 || instance <= separator+2 {
		return text
	}
	return text[separator+2 : instance]
}

func replaceString(values []string, old, replacement string) []string {
	result := append([]string(nil), values...)
	for index, value := range result {
		if value == old {
			result[index] = replacement
			return result
		}
	}
	return append(result, replacement)
}
