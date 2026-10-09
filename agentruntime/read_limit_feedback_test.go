package agentruntime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOversizedReadFeedbackRetainsStrictSchemaAndNoDispatch(t *testing.T) {
	for _, name := range []string{"read_file", "read_file_from_commit"} {
		contract, err := NewStructuredTurnContract([]ToolDefinition{
			{Name: name, Parameters: json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"line_count":{"type":"integer","minimum":1,"maximum":400}}}`)},
			{Name: "submit_result", Parameters: json.RawMessage(`{"type":"object"}`)},
		}, "submit_result")
		if err != nil {
			t.Fatal(err)
		}
		raw := json.RawMessage(`{"calls":[{"` + name + `":{"path":"handler.go","line_count":456}}]}`)
		completion, err := contract.decode(raw, "read-too-long")
		var rejected *RejectedTurnError
		if !errors.As(err, &rejected) || len(completion.ToolCalls) != 0 || !strings.Contains(rejected.Problem, "next_start_line") || !strings.Contains(rejected.Problem, "maximum") {
			t.Fatalf("%+v %v", completion, err)
		}
		if string(rejected.Response) != string(raw) {
			t.Fatal("lost exact rejected proposal")
		}
		completion, err = contract.decode(json.RawMessage(`{"calls":[{"`+name+`":{"path":"handler.go","line_count":400}}]}`), "read-fixed")
		if err != nil || len(completion.ToolCalls) != 1 {
			t.Fatalf("valid page rejected: %v", err)
		}
	}
}
