package nativeagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Model-facing structure supplements the accepted narrative contract without
// changing the signed role package or storing JSON hidden inside a string.
var constructionContractSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["provided_interface","required_inputs","consumer_usage","verification"],"properties":{"provided_interface":{"type":"string","minLength":1,"maxLength":1024,"description":"Concrete callable/data contract, including its input and output types; no requirement to predetermine ordinary implementation details."},"required_inputs":{"type":"array","maxItems":16,"description":"Every input or resource the consumer must acquire. Use [] if none. Name an accessible source, or the upstream task that supplies it; a private field is not an accessible source.","items":{"type":"object","additionalProperties":false,"required":["input","source"],"properties":{"input":{"type":"string","minLength":1,"maxLength":256},"source":{"type":"string","minLength":1,"maxLength":512}}}},"consumer_usage":{"type":"string","minLength":1,"maxLength":1024,"description":"How the consumer invokes or constructs the delivered capability within its assigned scope, without editing provider-owned files."},"verification":{"type":"string","minLength":1,"maxLength":1024,"description":"Provider-side verification that the actual public boundary is usable with those inputs. Interface assertions or nil/test-double construction alone are insufficient for runtime construction."}}}`)

func decodeConstructionContract(raw json.RawMessage) (string, error) {
	var c struct {
		ProvidedInterface string `json:"provided_interface"`
		RequiredInputs    []struct {
			Input  string `json:"input"`
			Source string `json:"source"`
		} `json:"required_inputs"`
		ConsumerUsage string `json:"consumer_usage"`
		Verification  string `json:"verification"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(&struct{}{}) != io.EOF || c.RequiredInputs == nil || len(c.RequiredInputs) > 16 || strings.TrimSpace(c.ProvidedInterface) == "" || len([]rune(c.ProvidedInterface)) > 1024 || strings.TrimSpace(c.ConsumerUsage) == "" || len([]rune(c.ConsumerUsage)) > 1024 || strings.TrimSpace(c.Verification) == "" || len([]rune(c.Verification)) > 1024 {
		return "", fmt.Errorf("%w: handoff contract must specify the provided interface, every required input and its accessible source, consumer usage, and verification", ErrInvalidPlanDelta)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Provided interface: %s\nRequired inputs:", c.ProvidedInterface)
	if len(c.RequiredInputs) == 0 {
		b.WriteString(" none")
	}
	for _, input := range c.RequiredInputs {
		if strings.TrimSpace(input.Input) == "" || len([]rune(input.Input)) > 256 || strings.TrimSpace(input.Source) == "" || len([]rune(input.Source)) > 512 {
			return "", ErrInvalidPlanDelta
		}
		fmt.Fprintf(&b, "\n- %s: %s", input.Input, input.Source)
	}
	fmt.Fprintf(&b, "\nConsumer usage: %s\nProvider verification: %s", c.ConsumerUsage, c.Verification)
	if b.Len() > 4096 {
		return "", fmt.Errorf("%w: handoff contract exceeds 4096 bytes", ErrInvalidPlanDelta)
	}
	return b.String(), nil
}
