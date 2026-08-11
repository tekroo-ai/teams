package kernel

import "errors"

type ReplayStatus string

const (
	ReplayApplied     ReplayStatus = "APPLIED"
	ReplayQuarantined ReplayStatus = "QUARANTINED"
)

type ReplayResult struct {
	Status           ReplayStatus
	LastRevision     uint64
	State            *AggregateState
	QuarantinedEvent *DomainEvent
	Reason           string
}

func ReplayAuthoritative(catalogue CatalogueSnapshot, events []DomainEvent) ReplayResult {
	for index, event := range events {
		_, err := catalogue.ResolveEvent(event.EventType, event.EventVersion, event.Aggregate.Kind, event.Payload)
		if err == nil {
			continue
		}
		result := ReplayResult{Status: ReplayQuarantined, LastRevision: uint64(index), QuarantinedEvent: cloneDomainEvent(&event), Reason: "INVALID_EVENT"}
		if errors.Is(err, ErrUnknownEvent) {
			result.Reason = "UNKNOWN_EVENT_TYPE"
		} else if errors.Is(err, ErrUnsupportedVersion) {
			result.Reason = "UNSUPPORTED_EVENT_VERSION"
		}
		if index > 0 {
			result.State, _ = FoldAggregate(events[:index])
		}
		return result
	}
	state, err := FoldAggregate(events)
	if err != nil {
		return ReplayResult{Status: ReplayQuarantined, Reason: "CORRUPT_EVENT_STREAM"}
	}
	return ReplayResult{Status: ReplayApplied, LastRevision: uint64(len(events)), State: state}
}

func cloneDomainEvent(event *DomainEvent) *DomainEvent {
	if event == nil {
		return nil
	}
	copy := *event
	copy.ActorFQN = cloneActor(event.ActorFQN)
	copy.Execution = cloneExecution(event.Execution)
	copy.Parents = append([]DagParent(nil), event.Parents...)
	copy.Payload = append([]byte(nil), event.Payload...)
	return &copy
}
