package agenttools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReadPaginationGuidanceKeepsStrict400LineLimit(t *testing.T) {
	for _, spec := range Available([]string{"repository.read"}) {
		if spec.Name != "read_file" && spec.Name != "read_file_from_commit" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Maximum     int
				Description string
			}
		}
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		count := schema.Properties["line_count"]
		if count.Maximum != 400 || !strings.Contains(count.Description, "PER CALL") || !strings.Contains(count.Description, "next_start_line") || !strings.Contains(spec.Description, "400") {
			t.Fatalf("%s: %+v", spec.Name, count)
		}
	}
}
