package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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

// CompareAndSwapPlanAmendment fences the graph read and the feature update in
// one majority transaction. Snapshot reads alone would permit write skew with
// concurrent task/invocation authorization. Touching a private fence counter
// creates a write conflict without modifying domain revisions or event history.
func (s *Store) CompareAndSwapPlanAmendment(ctx context.Context, next organization.FeatureRequest, expectedRevision uint64, fence organization.PlanAmendmentFence) (organization.FeatureRequest, error) {
	guards := fence.Preconditions
	if err := requireDeadline(ctx); err != nil {
		return organization.FeatureRequest{}, err
	}
	if s == nil || s.client == nil || expectedRevision == 0 || next.Revision != expectedRevision+1 || next.Validate() != nil || next.PlanAmendment == nil || !fence.Budget.Valid() || fence.Budget.ID != next.BudgetAccountID {
		return organization.FeatureRequest{}, organization.ErrPlanAmendment
	}
	byRef := make(map[kernel.AggregateRef]uint64, len(guards))
	for _, guard := range guards {
		if !guard.Aggregate.Valid() || guard.Expected.MustNotExist || guard.Expected.Revision == 0 || byRef[guard.Aggregate] != 0 {
			return organization.FeatureRequest{}, organization.ErrPlanAmendment
		}
		byRef[guard.Aggregate] = guard.Expected.Revision
	}
	record := next.PlanAmendment
	if (record.State == organization.AmendmentApproved || record.State == organization.AmendmentApplied) && (record.AssessmentInvocationID == "" || record.AssessmentInput == nil) {
		return organization.FeatureRequest{}, organization.ErrPlanAmendment
	}
	if record.AssessmentInvocationID != "" && (byRef[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: record.AssessmentID}] == 0 || byRef[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: record.AssessmentInvocationID}] == 0) {
		return organization.FeatureRequest{}, organization.ErrPlanAmendment
	}
	for _, task := range record.PriorPlan.Tasks {
		if byRef[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}] == 0 {
			return organization.FeatureRequest{}, organization.ErrPlanAmendment
		}
	}
	if byRef[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: record.BlockedTaskID}] != record.BlockedTaskRevision || byRef[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: record.ConsultationID}] == 0 || byRef[kernel.AggregateRef{Kind: kernel.AggregateWorkBudget, ID: next.BudgetAccountID}] == 0 || byRef[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: record.BlockedInvocationID}] == 0 || byRef[kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: record.ConsultationInvocationID}] == 0 {
		return organization.FeatureRequest{}, organization.ErrPlanAmendment
	}
	if record.State == organization.AmendmentApplied {
		for _, task := range next.Plan.Tasks {
			if byRef[kernel.AggregateRef{Kind: kernel.AggregateTask, ID: task.ID}] == 0 {
				return organization.FeatureRequest{}, organization.ErrPlanAmendment
			}
		}
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	session, err := s.client.StartSession()
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		var document featureDocument
		if err := s.db.Collection("feature_requests").FindOne(tx, bson.D{{Key: "_id", Value: string(next.ID)}, {Key: "revision", Value: expectedRevision}}).Decode(&document); err != nil {
			if errors.Is(err, driver.ErrNoDocuments) {
				return nil, organization.ErrFeatureRevisionConflict
			}
			return nil, err
		}
		prior, err := decodeFeature(document)
		if err != nil {
			return nil, err
		}
		if !organization.ValidPlanAmendmentTransition(prior, next) {
			return nil, organization.ErrPlanAmendment
		}
		var budgetDocument valueDocument
		budgetKey := aggregateKey(fence.Budget.Ref())
		if err := s.db.Collection("work_budget_accounts").FindOne(tx, bson.D{{Key: "_id", Value: budgetKey}}).Decode(&budgetDocument); err != nil {
			return nil, err
		}
		var budgetValue workBudgetValue
		if err := decode(budgetDocument.Data, &budgetValue); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(budgetValue.Account, fence.Budget) || byRef[fence.Budget.Ref()] != fence.Budget.Revision {
			return nil, organization.ErrFeatureRevisionConflict
		}
		// Debits do not bump the policy revision. Lock the value document, not
		// just its aggregate header, against concurrent invocation admission.
		if _, err := s.db.Collection("work_budget_accounts").UpdateOne(tx, bson.D{{Key: "_id", Value: budgetKey}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "plan_amendment_fence", Value: int64(1)}}}}); err != nil {
			return nil, err
		}
		invocationIDs := []kernel.UUIDv7{record.BlockedInvocationID, record.ConsultationInvocationID}
		if record.AssessmentInvocationID != "" {
			invocationIDs = append(invocationIDs, record.AssessmentInvocationID)
		}
		for _, id := range invocationIDs {
			var document valueDocument
			if err := s.db.Collection("work_invocations").FindOne(tx, bson.D{{Key: "_id", Value: aggregateKey(kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: id})}}).Decode(&document); err != nil {
				return nil, err
			}
			var value invocationValue
			if err := decode(document.Data, &value); err != nil {
				return nil, err
			}
			assessment := id == record.AssessmentInvocationID
			validTerminal := value.Invocation.State == kernel.InvocationSucceeded || assessment && record.State == organization.AmendmentRejected && value.Invocation.State.Terminal()
			failedAssessment := assessment && record.State == organization.AmendmentRejected && value.Invocation.State.Terminal() && value.Invocation.State != kernel.InvocationSucceeded
			if value.Reference.Kind != kernel.AggregateWorkInvocation || value.Reference.ID != id || value.Invocation.ID != id || !validTerminal || !failedAssessment && (value.Invocation.OutputDigest == nil || len(value.Invocation.TerminalEvidenceIDs) == 0) || value.Invocation.Revision != byRef[value.Reference] {
				return nil, organization.ErrFeatureRevisionConflict
			}
			if id == record.ConsultationInvocationID && (value.Invocation.TaskID != record.ConsultationID || *value.Invocation.OutputDigest != record.AnswerDigest) {
				return nil, organization.ErrPlanAmendment
			}
			if id == record.BlockedInvocationID && value.Invocation.TaskID != record.BlockedTaskID {
				return nil, organization.ErrPlanAmendment
			}
			if assessment && (value.Invocation.TaskID != record.AssessmentID || value.Invocation.ActorFQN != record.PriorPlan.PreparedBy || value.Invocation.Purpose != kernel.PurposeHandoff || value.Invocation.AttemptOrdinal != 1 || value.Invocation.BudgetAccountID != next.BudgetAccountID || value.Invocation.OutputDigest == nil && record.AssessmentOutputDigest != "" || value.Invocation.OutputDigest != nil && *value.Invocation.OutputDigest != record.AssessmentOutputDigest || record.Decision.Accepted && value.Invocation.Execution != record.Proposal.SuccessorPlan.PreparedExecution) {
				return nil, organization.ErrPlanAmendment
			}
			outputBound := false
			for _, evidenceID := range value.Invocation.TerminalEvidenceIDs {
				var metadata evidenceDocument
				if err := s.db.Collection("evidence").FindOne(tx, bson.D{{Key: "_id", Value: string(evidenceID)}, {Key: "available", Value: true}}).Decode(&metadata); err != nil {
					return nil, err
				}
				if !kernel.Digest(metadata.SHA256).Valid() {
					return nil, organization.ErrPlanAmendment
				}
				outputBound = outputBound || value.Invocation.OutputDigest != nil && kernel.Digest(metadata.SHA256) == *value.Invocation.OutputDigest
				result, err := s.db.Collection("evidence").UpdateOne(tx, bson.D{{Key: "_id", Value: string(evidenceID)}, {Key: "available", Value: true}, {Key: "sha256", Value: metadata.SHA256}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "plan_amendment_fence", Value: int64(1)}}}})
				if err != nil {
					return nil, err
				}
				if result.MatchedCount != 1 {
					return nil, organization.ErrFeatureRevisionConflict
				}
			}
			if !outputBound && !failedAssessment {
				return nil, organization.ErrPlanAmendment
			}
		}
		if record.AssessmentInput != nil {
			result, err := s.db.Collection("evidence").UpdateOne(tx, bson.D{{Key: "_id", Value: string(record.AssessmentInput.EvidenceID)}, {Key: "available", Value: true}, {Key: "sha256", Value: string(record.AssessmentInput.SHA256)}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "plan_amendment_fence", Value: int64(1)}}}})
			if err != nil {
				return nil, err
			}
			if result.MatchedCount != 1 {
				return nil, organization.ErrPlanAmendment
			}
		}
		if (record.State == organization.AmendmentApproved || record.State == organization.AmendmentApplied) && !time.Now().UTC().Before(record.DeadlineAt) {
			return nil, organization.ErrPlanAmendment
		}
		for _, guard := range guards {
			result, err := s.db.Collection("aggregates").UpdateOne(tx, bson.D{{Key: "_id", Value: aggregateKey(guard.Aggregate)}, {Key: "revision", Value: guard.Expected.Revision}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "plan_amendment_fence", Value: int64(1)}}}})
			if err != nil {
				return nil, err
			}
			if result.MatchedCount != 1 {
				return nil, organization.ErrFeatureRevisionConflict
			}
		}
		result, err := s.db.Collection("feature_requests").UpdateOne(tx, bson.D{{Key: "_id", Value: string(next.ID)}, {Key: "revision", Value: expectedRevision}}, bson.D{{Key: "$set", Value: bson.D{{Key: "revision", Value: next.Revision}, {Key: "status", Value: next.Status}, {Key: "updated_at", Value: next.UpdatedAt}, {Key: "data", Value: raw}}}})
		if err != nil {
			return nil, err
		}
		if result.ModifiedCount != 1 {
			return nil, organization.ErrFeatureRevisionConflict
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
	if err != nil {
		return organization.FeatureRequest{}, err
	}
	return next, nil
}

var _ organization.PlanAmendmentStore = (*Store)(nil)
