package memory

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"

	"github.com/tekroo-ai/teams/eventexport"
)

type EventExportSource struct {
	mu         sync.RWMutex
	sourceID   string
	generation uint64
	records    [][]byte
	onOpen     func(string)
}

func NewEventExportSource(sourceID string) (*EventExportSource, error) {
	if sourceID == "" {
		return nil, eventexport.ErrInvalidRequest
	}
	return &EventExportSource{sourceID: sourceID, generation: 1}, nil
}

func (source *EventExportSource) Append(eventBytes []byte) error {
	if _, err := eventexport.DecodeDomainEvent(eventBytes); err != nil {
		return err
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	source.records = append(source.records, append([]byte(nil), eventBytes...))
	return nil
}

func (source *EventExportSource) ResetHistory() {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.generation++
}

func (source *EventExportSource) SetOpenObserver(observer func(string)) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.onOpen = observer
}

func (source *EventExportSource) Open(ctx context.Context, sourceID string, position []byte) (eventexport.Feed, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if sourceID != source.sourceID {
		return nil, eventexport.ErrUnauthorized
	}
	index := uint64(0)
	if len(position) > 0 {
		if len(position) != 16 || binary.BigEndian.Uint64(position[:8]) != source.generation {
			return nil, eventexport.ErrResyncRequired
		}
		index = binary.BigEndian.Uint64(position[8:])
		if index > uint64(len(source.records)) {
			return nil, eventexport.ErrResyncRequired
		}
	}
	if source.onOpen != nil {
		source.onOpen("LIVE_OPENED")
	}
	backlogEnd := uint64(len(source.records))
	if source.onOpen != nil {
		source.onOpen("BACKLOG_SNAPSHOTTED")
	}
	return &memoryEventFeed{source: source, generation: source.generation, index: index, backlogEnd: backlogEnd}, nil
}

type memoryEventFeed struct {
	source     *EventExportSource
	generation uint64
	index      uint64
	backlogEnd uint64
	closed     bool
}

func (feed *memoryEventFeed) Next(ctx context.Context) (eventexport.RawRecord, error) {
	if err := ctx.Err(); err != nil {
		return eventexport.RawRecord{}, err
	}
	feed.source.mu.RLock()
	defer feed.source.mu.RUnlock()
	if feed.closed {
		return eventexport.RawRecord{}, errors.New("event export feed is closed")
	}
	if feed.generation != feed.source.generation {
		return eventexport.RawRecord{}, eventexport.ErrResyncRequired
	}
	if feed.index >= uint64(len(feed.source.records)) {
		return eventexport.RawRecord{}, eventexport.ErrCaughtUp
	}
	bytes := append([]byte(nil), feed.source.records[feed.index]...)
	feed.index++
	return eventexport.RawRecord{Bytes: bytes, Position: feed.positionLocked()}, nil
}

func (feed *memoryEventFeed) Position() []byte {
	feed.source.mu.RLock()
	defer feed.source.mu.RUnlock()
	return feed.positionLocked()
}

func (feed *memoryEventFeed) positionLocked() []byte {
	position := make([]byte, 16)
	binary.BigEndian.PutUint64(position[:8], feed.generation)
	binary.BigEndian.PutUint64(position[8:], feed.index)
	return position
}

func (feed *memoryEventFeed) Close(context.Context) error {
	feed.source.mu.Lock()
	defer feed.source.mu.Unlock()
	feed.closed = true
	return nil
}
