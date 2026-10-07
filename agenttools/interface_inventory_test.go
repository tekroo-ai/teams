package agenttools

import (
	"strings"
	"testing"
)

func TestExportedGoDeclarationsAreConcreteSignaturesNotBodiesOrComments(t *testing.T) {
	content := []byte("package p\n// ImaginaryAPI is only a comment.\nfunc Actual(value string) (int,error) { return 0,nil }\nfunc private() {}\ntype Exported struct { Value string }\n")
	declarations, err := ExportedGoDeclarations("api.go", content)
	if err != nil || len(declarations) != 2 {
		t.Fatalf("declarations=%v error=%v", declarations, err)
	}
	if declarations[0].Symbol != "Actual" || declarations[0].Line != 3 || !strings.Contains(declarations[0].Signature, "(value string) (int, error)") || strings.Contains(declarations[0].Signature, "return") {
		t.Fatalf("function=%+v", declarations[0])
	}
	if declarations[1].Symbol != "Exported" {
		t.Fatalf("type=%+v", declarations[1])
	}
	for _, path := range []string{"../outside.go", ".git/config", "/absolute.go"} {
		if _, err := ExportedGoDeclarations(path, content); err == nil {
			t.Fatalf("unsafe path %s", path)
		}
	}
}
