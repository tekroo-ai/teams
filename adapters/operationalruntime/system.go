package operationalruntime

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type UUIDv7Source struct {
	clock kernel.Clock
}

func NewUUIDv7Source(clock kernel.Clock) (*UUIDv7Source, error) {
	if clock == nil {
		return nil, application.ErrInvalidConfiguration
	}
	return &UUIDv7Source{clock: clock}, nil
}

func (source *UUIDv7Source) Next() (kernel.UUIDv7, error) {
	if source == nil || source.clock == nil {
		return "", application.ErrInvalidConfiguration
	}
	var value [16]byte
	if _, err := rand.Read(value[6:]); err != nil {
		return "", err
	}
	milliseconds := uint64(source.clock.Now().UnixMilli())
	value[0] = byte(milliseconds >> 40)
	value[1] = byte(milliseconds >> 32)
	value[2] = byte(milliseconds >> 24)
	value[3] = byte(milliseconds >> 16)
	value[4] = byte(milliseconds >> 8)
	value[5] = byte(milliseconds)
	value[6] = value[6]&0x0f | 0x70
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return kernel.UUIDv7(encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]), nil
}

var (
	_ kernel.Clock    = SystemClock{}
	_ kernel.IDSource = (*UUIDv7Source)(nil)
)
