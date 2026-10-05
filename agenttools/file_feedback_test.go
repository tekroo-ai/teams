package agenttools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileIdentifiesCompleteContentAndExcerpts(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("line\n", 500)
	writeSnapshotFixture(t, root, "large.txt", content)
	writeSnapshotFixture(t, root, "small.txt", "one\ntwo\nthree\n")
	writeSnapshotFixture(t, root, "empty.txt", "")
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	for _, test := range []struct {
		args              string
		path              string
		start, end, count int
		complete          bool
	}{
		{`{"path":"large.txt"}`, "large.txt", 1, 400, 500, false},
		{`{"path":"large.txt","start_line":401,"end_line":500}`, "large.txt", 401, 500, 500, false},
		{`{"path":"small.txt"}`, "small.txt", 1, 3, 3, true},
		{`{"path":"small.txt","start_line":2,"end_line":10}`, "small.txt", 2, 3, 3, false},
		{`{"path":"small.txt","start_line":4,"end_line":10}`, "small.txt", 0, 0, 3, false},
		{`{"path":"empty.txt"}`, "empty.txt", 0, 0, 0, true},
	} {
		result, err := testHost().execute(t.Context(), authority, call("read_file", test.args))
		if err != nil || result.Path != test.path || result.StartLine == nil || *result.StartLine != test.start || result.EndLine == nil || *result.EndLine != test.end || result.LineCount == nil || *result.LineCount != test.count || result.ContentComplete == nil || *result.ContentComplete != test.complete || result.Truncated == test.complete || result.MaxReadLines != maxReadFileLines {
			t.Fatalf("file response %s: %+v %v", test.args, result, err)
		}
		data, err := os.ReadFile(filepath.Join(root, test.path))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		if result.SHA256 != hex.EncodeToString(hash[:]) {
			t.Fatal("excerpt hash no longer identifies the full file")
		}
	}
	_, err := testHost().execute(t.Context(), authority, call("read_file", `{"path":"large.txt","start_line":120,"end_line":2001}`))
	if !errors.Is(err, ErrInvalidCall) || !strings.Contains(err.Error(), "400 lines") || !strings.Contains(err.Error(), `path="large.txt"`) {
		t.Fatalf("wide read did not explain its bound: %v", err)
	}
}

func TestWriteConflictReportsCorrectPathWithoutWritingOrReserving(t *testing.T) {
	gateway, ledger, root, request := effectFixture(t)
	writeSnapshotFixture(t, root, "a.txt", "first file")
	writeSnapshotFixture(t, root, "b.txt", "second file")
	wrong := sha256.Sum256([]byte("second file"))
	actual := sha256.Sum256([]byte("first file"))
	request.Call.Arguments, _ = json.Marshal(map[string]string{"path": "a.txt", "content": "replacement", "expected_sha256": hex.EncodeToString(wrong[:])})
	_, err := gateway.Execute(t.Context(), request)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), `path="a.txt"`) || !strings.Contains(err.Error(), hex.EncodeToString(actual[:])) || !strings.Contains(err.Error(), hex.EncodeToString(wrong[:])) {
		t.Fatalf("wrong-file hash conflict lacks target evidence: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil || string(data) != "first file" || len(ledger.records) != 0 {
		t.Fatalf("rejected write changed state: %q %v %+v", data, err, ledger.records)
	}
}
