package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestNextReviewRepairRoundLinksLatestRepair(t *testing.T) {
	taskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000401")
	otherTaskID := kernel.UUIDv7("00000000-0000-7000-8000-000000000402")
	firstCondition := repeatedDigest('a')
	secondCondition := repeatedDigest('b')
	thirdCondition := repeatedDigest('c')
	firstID := kernel.UUIDv7("00000000-0000-7000-8000-000000000403")
	secondID := kernel.UUIDv7("00000000-0000-7000-8000-000000000404")
	invocations := map[kernel.AggregateRef]kernel.WorkInvocation{
		{Kind: kernel.AggregateWorkInvocation, ID: firstID}:                                {ID: firstID, TaskID: taskID, Purpose: kernel.PurposeRepair, AttemptOrdinal: 1, ConditionDigest: firstCondition, Revision: 2},
		{Kind: kernel.AggregateWorkInvocation, ID: secondID}:                               {ID: secondID, TaskID: taskID, Purpose: kernel.PurposeRepair, AttemptOrdinal: 2, ConditionDigest: secondCondition, Revision: 2},
		{Kind: kernel.AggregateWorkInvocation, ID: "00000000-0000-7000-8000-000000000405"}: {TaskID: otherTaskID, Purpose: kernel.PurposeRepair, AttemptOrdinal: 9, ConditionDigest: thirdCondition},
		{Kind: kernel.AggregateWorkInvocation, ID: "00000000-0000-7000-8000-000000000406"}: {TaskID: taskID, Purpose: kernel.PurposeValidation, AttemptOrdinal: 9, ConditionDigest: thirdCondition},
	}

	if round, prior, duplicate := nextReviewRepairRound(nil, taskID, firstCondition); round != 1 || prior != nil || duplicate {
		t.Fatalf("first round: round=%d prior=%v duplicate=%t", round, prior, duplicate)
	}
	if round, prior, duplicate := nextReviewRepairRound(invocations, taskID, thirdCondition); round != 3 || prior == nil || prior.ID != secondID || duplicate {
		t.Fatalf("third round: round=%d prior=%v duplicate=%t", round, prior, duplicate)
	}
	if round, prior, duplicate := nextReviewRepairRound(invocations, taskID, firstCondition); round != 0 || prior != nil || !duplicate {
		t.Fatalf("duplicate condition: round=%d prior=%v duplicate=%t", round, prior, duplicate)
	}
}
