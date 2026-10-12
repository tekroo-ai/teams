package operationalruntime

import (
	"context"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type terminalReaderProbe struct {
	application.OperationalExecutionReader
	calls         int
	identityCalls int
}

func (p *terminalReaderProbe) ReadInvocationIDForAuthorization(context.Context, kernel.UUIDv7) (kernel.UUIDv7, bool, error) {
	p.identityCalls++
	return "00000000-0000-7000-8000-000000000001", true, nil
}

func (p *terminalReaderProbe) ReadCommittedTerminalDecision(context.Context, kernel.UUIDv7) (application.CommittedTerminalDecision, bool, error) {
	p.calls++
	return application.CommittedTerminalDecision{Invocation: kernel.WorkInvocation{ID: "00000000-0000-7000-8000-000000000001"}}, true, nil
}

type terminalReaderDeadline struct{}

func (terminalReaderDeadline) EffectiveWorkInvocationDeadline(context.Context, kernel.WorkInvocation, time.Time) (time.Time, error) {
	return time.Now(), nil
}
func TestAssembledReaderRetainsTerminalProofWithAndWithoutDeadlineExtension(t *testing.T) {
	for _, deadline := range []application.OperationalDeadlineExtensionReader{nil, terminalReaderDeadline{}} {
		p := &terminalReaderProbe{}
		reader := assembleOperationalExecutionReader(p, deadline)
		proof, ok := reader.(application.OperationalTerminalDecisionReader)
		if !ok {
			t.Fatal("terminal proof capability dropped")
		}
		v, found, err := proof.ReadCommittedTerminalDecision(context.Background(), "00000000-0000-7000-8000-000000000001")
		if err != nil || !found || !v.Invocation.ID.Valid() || p.calls != 1 {
			t.Fatalf("proof=%+v found=%v calls=%d err=%v", v, found, p.calls, err)
		}
		_, hasDeadline := reader.(application.OperationalDeadlineExtensionReader)
		if hasDeadline != (deadline != nil) {
			t.Fatal("unconfigured deadline capability exposed")
		}
		id, found, err := reader.(application.OperationalAuthorizationIdentityReader).ReadInvocationIDForAuthorization(context.Background(), "00000000-0000-7000-8000-000000000001")
		if err != nil || !found || !id.Valid() || p.identityCalls != 1 {
			t.Fatal("identity lookup capability dropped")
		}
	}
	reader := assembleOperationalExecutionReader(terminalReaderProbe{}.OperationalExecutionReader, nil)
	_, found, err := reader.(application.OperationalTerminalDecisionReader).ReadCommittedTerminalDecision(context.Background(), "00000000-0000-7000-8000-000000000001")
	if err != nil || found {
		t.Fatal("missing underlying proof manufactured a result")
	}
}
