package application

import (
	"context"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type HumanDeliveryRequest struct {
	InteractionID           kernel.UUIDv7
	QuestionRevision        uint64
	Recipient               kernel.PrincipalRef
	DeliveryBinding         kernel.HumanDeliveryBinding
	CanonicalQuestionDigest kernel.Digest
	Confidentiality         kernel.Confidentiality
	DeadlineAt              time.Time
}

type HumanDeliveryObservation struct {
	DeliveryID                    kernel.UUIDv7
	Outcome                       string
	RenderedQuestionDigest        kernel.Digest
	MaterialEquivalenceEvidenceID *kernel.UUIDv7
	AttemptedAt                   time.Time
	ChannelProvenanceDigest       kernel.Digest
	EvidenceIDs                   []kernel.UUIDv7
}

// HumanDeliveryPort may deliver and observe, but its observation is evidence;
// it cannot create a HUMAN principal, response, consent, or authority.
type HumanDeliveryPort interface {
	Deliver(context.Context, HumanDeliveryRequest) (HumanDeliveryObservation, error)
}

type HumanCredentialPresentation struct {
	Participant             kernel.PrincipalRef
	AuthenticationBindingID kernel.UUIDv7
	CredentialDigest        kernel.Digest
	ChannelProvenanceDigest kernel.Digest
	PresentedAt             time.Time
}

type HumanAuthenticationProof struct {
	Participant                kernel.PrincipalRef
	AuthenticationBindingID    kernel.UUIDv7
	CredentialProvenanceDigest kernel.Digest
	Assurance                  string
	EvidenceIDs                []kernel.UUIDv7
}

// HumanAuthenticationPort verifies an exact participant binding. A shared
// endpoint, display label, delivery receipt, or operator relay is insufficient.
type HumanAuthenticationPort interface {
	VerifyHuman(context.Context, HumanCredentialPresentation) (HumanAuthenticationProof, error)
}

type HumanParticipantDirectory interface {
	ResolveParticipant(context.Context, kernel.PrincipalRef) (kernel.AggregateRef, kernel.HumanParticipantSnapshot, error)
}
