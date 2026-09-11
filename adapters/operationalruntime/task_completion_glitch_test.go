package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestStructuredOutputGlitchRetryEligibility(t *testing.T) {
	const limit = uint32(3)
	succeeded := kernel.WorkInvocation{State: kernel.InvocationSucceeded, AttemptOrdinal: 1}
	validator := organization.PlannedTask{Purpose: kernel.PurposeValidation, AttemptLimit: limit, Validates: []kernel.UUIDv7{kernel.UUIDv7("00000000-0000-7000-8000-0000000000a1")}}
	review := validator
	review.Purpose = kernel.PurposeReview
	promotion := organization.PlannedTask{Purpose: kernel.PurposePromotion, AttemptLimit: limit}
	implementation := organization.PlannedTask{Purpose: kernel.PurposeImplementation, AttemptLimit: limit}

	if !structuredOutputGlitchRetryAllowed(validator, succeeded) {
		t.Error("a validator glitch within the attempt limit must be retryable")
	}
	if !structuredOutputGlitchRetryAllowed(review, succeeded) {
		t.Error("a review glitch within the attempt limit must be retryable")
	}
	if !structuredOutputGlitchRetryAllowed(promotion, succeeded) {
		t.Error("a promotion glitch within the attempt limit must be retryable")
	}
	if structuredOutputGlitchRetryAllowed(implementation, succeeded) {
		t.Error("implementation output is not glitch-retryable")
	}
	unvalidated := validator
	unvalidated.Validates = nil
	if structuredOutputGlitchRetryAllowed(unvalidated, succeeded) {
		t.Error("a validation task without a validates binding is not glitch-retryable")
	}
	exhausted := succeeded
	exhausted.AttemptOrdinal = uint64(limit)
	if structuredOutputGlitchRetryAllowed(validator, exhausted) {
		t.Error("glitch retry must stop at the planned attempt limit")
	}
	if structuredOutputGlitchRetryAllowed(validator, kernel.WorkInvocation{State: kernel.InvocationFailed, AttemptOrdinal: 1}) {
		t.Error("a failed invocation is recovered by the terminal path, not the glitch path")
	}
}
