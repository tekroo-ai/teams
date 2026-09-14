package operationalruntime

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/tekroo-ai/teams/application"
)

func roleHandlerWorkProduct(output []byte) (json.RawMessage, bool) {
	marker := []byte(application.OrganizationalResultMarker)
	index := bytes.LastIndex(output, marker)
	if index < 0 || bytes.Count(output, marker) != 1 || index > 0 && output[index-1] != '\n' {
		return nil, false
	}
	payload := bytes.TrimSpace(output[index+len(marker):])
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var envelope struct {
		WorkProduct json.RawMessage `json:"work_product"`
	}
	if decoder.Decode(&envelope) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(envelope.WorkProduct) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), envelope.WorkProduct...), true
}
