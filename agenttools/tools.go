// Package agenttools defines Teams-owned, provider-neutral agent tool contracts
// and a bounded local implementation. The public gateway reconstructs authority
// from an admitted Teams execution; transports cannot supply it themselves.
package agenttools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrInvalidCall = errors.New("invalid agent tool call")
	ErrForbidden   = errors.New("agent tool is not authorized")
	ErrBoundary    = errors.New("path escapes the assigned workspace")
	ErrTooLarge    = errors.New("agent tool output exceeds its bound")
	ErrConflict    = errors.New("agent tool file precondition does not match")
)

// Spec is the Teams-owned contract intended for model exposure and server-side
// dispatch. A future model-facing adapter may translate InputSchema to its wire
// format, but must not independently redefine parameters or permissions.
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Permission  string          `json:"-"`
}

var specs = []Spec{
	{"make_directory", "Create a workspace directory and missing parents. Existing directories succeed; files, symlinks and reserved paths are rejected. Never deletes or overwrites. Returns a durable effect receipt.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1,"maxLength":1024}}}`), "repository.edit"},
	{"file_change_history", "Read up to 20 retained successful file changes in this authorized workspace. Returns change IDs, recorded postcondition hashes and whether exact undo is currently applicable. Never returns preimage contents.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1}}}`), "repository.read"},
	{"format_go_files", "Apply gofmt to named Go files against their exact current SHA-256 hashes. All inputs must validate before mutation. Returns one change_id and aggregate SHA-256 for exact undo_file_change; syntax failures leave files unchanged.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["files"],"properties":{"files":{"type":"array","minItems":1,"maxItems":16,"items":{"type":"object","additionalProperties":false,"required":["path","expected_sha256"],"properties":{"path":{"type":"string","minLength":1},"expected_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"}}}}}}`), "repository.edit"},
	{"move_file", "Move one regular workspace file to a new path without overwriting. Requires current source SHA-256; destination parent must exist. Returns change_id and aggregate postcondition SHA-256 for exact undo.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["source","destination","expected_sha256"],"properties":{"source":{"type":"string","minLength":1},"destination":{"type":"string","minLength":1},"expected_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"}}}`), "repository.edit"},
	{"remove_file", "Remove one regular workspace file against its current SHA-256, retaining exact bytes for undo. Never removes directories or recursively deletes. Returns change_id and aggregate postcondition SHA-256 for exact undo.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path","expected_sha256"],"properties":{"path":{"type":"string","minLength":1},"expected_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"}}}`), "repository.edit"},
	{"find_definitions", "Find Go declarations for one exact symbol in a workspace directory. Returns signatures and file/line locations from parsed source, not text matches. Reports skipped/unparseable files; bounded to 100 declarations.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["symbol"],"properties":{"symbol":{"type":"string","pattern":"^[A-Za-z_][A-Za-z0-9_]*$"},"directory":{"type":"string"}}}`), "repository.read"},
	{"find_references", "Find semantically resolved Go references to the declaration at path and line. Returns bounded file/line locations and explicitly reports unresolved packages. Never labels plain text matches as symbol references.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path","line"],"properties":{"path":{"type":"string","minLength":1},"line":{"type":"integer","minimum":1}}}`), "repository.read"},
	{"check_go_build_worktree", "Compile production and test packages across the module from a disposable working-tree snapshot without executing candidate binaries, initializers or TestMain. Returns snapshot identity and compiler diagnostics. This is a compile check, not a test-suite or acceptance verdict.", json.RawMessage(`{"type":"object","additionalProperties":false}`), "test.execute"},
	{"read_file", "Read one workspace file starting at start_line (default 1), for line_count lines (default 400, maximum 400). Returns total file line_count, full-file SHA-256, actual start/end lines, and next_start_line when more lines remain. Stops at EOF. A missing file returns status NOT_FOUND without reading any alternative; use find_files to discover its actual path. Use read_evidence for evidence IDs.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1,"description":"Relative path to a workspace file; never an evidence_id."},"start_line":{"type":"integer","minimum":1,"default":1},"line_count":{"type":"integer","minimum":1,"maximum":400,"default":400,"description":"Number of lines requested, not an ending line number."}}}`), "repository.read"},
	{"check_go_format", "Check whether named workspace Go files are gofmt-clean without changing them; format_clean is true only when all named files match gofmt output.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["paths"],"properties":{"paths":{"type":"array","minItems":1,"maxItems":32,"uniqueItems":true,"items":{"type":"string","minLength":1}}}}`), "repository.read"},
	{"list_files", "List one workspace directory without recursing. If truncated, continue with next_after as start_after.", json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"start_after":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200}}}`), "repository.read"},
	{"find_files", "Find workspace files matching a filename glob; ** matches directories. Results are bounded and sorted.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["filename_glob"],"properties":{"filename_glob":{"type":"string","minLength":1,"maxLength":256},"directory":{"type":"string"},"start_after":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200},"summary":{"type":"string"}}}`), "repository.read"},
	{"search_text", "Find literal text in workspace files. Punctuation such as parentheses, brackets and | is literal; no escaping or regex syntax is needed. Select matching lines, filenames, or a total count. Results are bounded and skipped files reported.", searchInputSchema("text"), "repository.read"},
	{"search_regex", "Search workspace text files with a deliberate RE2 regular expression (use | for alternatives and escape literal punctuation). For ordinary literal text, use search_text. Select matching lines, filenames, or a total count. Invalid regex syntax is reported precisely, never reinterpreted as literal text.", searchInputSchema("regex"), "repository.read"},
	{"git_status", "Read the worktree's branch, HEAD, and Git status without changing it; clean reports whether porcelain status has no tracked or untracked file entries.", json.RawMessage(`{"type":"object","additionalProperties":false}`), "repository.read"},
	{"git_diff", "Read a bounded Git diff for the worktree, index, or specified commits; optionally narrow it to one path.", json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","minLength":1},"from_commit":{"type":"string","pattern":"^(HEAD|[0-9a-f]{7,40})$"},"to_commit":{"type":"string","pattern":"^(HEAD|[0-9a-f]{7,40})$"},"staged":{"type":"boolean"},"format":{"type":"string","enum":["patch","stat","name_status","numstat","name_only"]}}}`), "repository.read"},
	{"git_log", "Read up to 20 recent commits without changing the repository.", json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":20},"format":{"type":"string","enum":["oneline","message"]}}}`), "repository.read"},
	{"git_show", "Read a bounded commit patch or summary, optionally for one path.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["commit"],"properties":{"commit":{"type":"string","pattern":"^(HEAD|[0-9a-f]{7,40})$"},"path":{"type":"string","minLength":1},"format":{"type":"string","enum":["patch","stat"]}}}`), "repository.read"},
	{"git_check_ignore", "Check whether one workspace path is ignored by Git.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1}}}`), "repository.read"},
	{"create_file", "Create ONE new file. Refuses any existing file; it cannot overwrite. Use edit_file for existing files. Returns change_id for exact undo.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path","content"],"properties":{"path":{"type":"string","minLength":1},"content":{"type":"string","maxLength":1048576}}}`), "repository.edit"},
	{"undo_file_change", "Undo one successful file change using its returned change_id and sha256 as a PAIR from that SAME original operation (or its file_change_history entry). expected_sha256 is that operation's recorded postcondition, which may cover several paths; it is NOT the latest file hash or a hash returned by a different undo. Restores exact recorded bytes/paths, or removes a newly created file; never reconstruct content. Refuses if any affected path differs from the recorded postcondition. Does not change the Git index or commits.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["change_id","expected_sha256"],"properties":{"change_id":{"type":"string","pattern":"^[0-9a-f]{64}$","description":"change_id returned by the original change to undo."},"expected_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$","description":"sha256 returned by that SAME original change, or its postcondition_sha256 in file_change_history. Never substitute another operation's hash."}}}`), "repository.edit"},
	{"restore_file_from_commit", "Restore ONE existing file from an exact commit SHA in this workspace's HEAD ancestry. Copies recorded Git bytes, not model-generated content. Requires the current file SHA-256 and preserves Git index/commits. Returns change_id for undo.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path","commit","expected_sha256"],"properties":{"path":{"type":"string","minLength":1},"commit":{"type":"string","pattern":"^[0-9a-f]{40}$"},"expected_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"}}}`), "repository.edit"},
	{"read_file_from_commit", "Read the actual file contents at an exact commit SHA in HEAD ancestry, even when that commit did not change the file. This is not a patch. Returns SHA-256 and bounded lines like read_file.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path","commit"],"properties":{"path":{"type":"string","minLength":1},"commit":{"type":"string","pattern":"^[0-9a-f]{40}$"},"start_line":{"type":"integer","minimum":1},"line_count":{"type":"integer","minimum":1,"maximum":400}}}`), "repository.read"},
	{"edit_file", "Atomically apply an ordered edits array to ONE existing file against ONE full-file SHA-256 from read_file. Each edit replaces exactly one occurrence of old_text with new_text in the preceding edit's result; new_text may be empty for deletion. Put all replacements for this file in this one call, not separate same-file calls with the same hash. Whole-file replacement/deletion is forbidden. Successful mutations return change_id; undo_file_change restores their exact pre-edit bytes without reconstruction. Every edit must match before anything is written; missing or ambiguous text and stale hashes leave the entire file unchanged.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path","edits","expected_sha256"],"properties":{"path":{"type":"string","minLength":1},"edits":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["old_text","new_text"],"properties":{"old_text":{"type":"string","minLength":1,"maxLength":1048576},"new_text":{"type":"string","maxLength":1048576}}}},"expected_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$","minLength":64,"maxLength":64}}}`), "repository.edit"},
	{"git_stage_files", "Stage only the named workspace paths when HEAD still matches the expected commit. Never stages the injected .openhands runtime files.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["paths","expected_head"],"properties":{"paths":{"type":"array","minItems":1,"maxItems":32,"uniqueItems":true,"items":{"type":"string","minLength":1}},"expected_head":{"type":"string","pattern":"^[0-9a-f]{40}$"}}}`), "repository.edit"},
	{"git_commit", "Commit the exact staged tree against an expected HEAD. A retry after a completed identical commit returns that commit instead of creating another.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["expected_head","expected_index_tree","subject"],"properties":{"expected_head":{"type":"string","pattern":"^[0-9a-f]{40}$"},"expected_index_tree":{"type":"string","pattern":"^[0-9a-f]{40}$"},"subject":{"type":"string","minLength":1,"maxLength":200},"body":{"type":"string","maxLength":5000}}}`), "repository.edit"},
	{"run_go_tests", "Run Go tests from committed HEAD in a disposable macOS filesystem sandbox. package may be ., ./, ./..., a local package such as ./adapters/mongo, or a subtree such as ./adapters/mongo/...; this workspace's declared module paths also work. Local Git subprocesses and localhost networking are supported for fixtures; use temporary test data, not live databases. Non-loopback networking, external modules, traversal, shell expansion, host writes and CGO are forbidden. permission_denial reports observed OS permission-denial output; it does not prove a candidate defect.", goTestInputSchema, "test.execute"},
	{"run_go_tests_worktree", "Run Go tests against a tracked-and-untracked workspace snapshot in a disposable macOS filesystem sandbox before committing. package may be ., ./, ./..., a local package such as ./adapters/mongo, or a subtree such as ./adapters/mongo/...; this workspace's declared module paths also work. Local Git subprocesses and localhost networking are supported for fixtures; use temporary test data, not live databases. Non-loopback networking, external modules and traversal are forbidden. Returns snapshot_sha256; independent validation uses run_go_tests on committed HEAD. permission_denial reports observed OS permission-denial output, not proof of a candidate defect.", goTestInputSchema, "test.execute"},
}

// Both test tools expose the same syntax. Module ownership and recursive
// subtree containment remain host checks, not model-supplied authority.
var goTestInputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["package"],"properties":{"package":{"type":"string","pattern":"^(?:\\.|\\./(?:\\.\\.\\.|[A-Za-z0-9_-][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_-][A-Za-z0-9_.-]*)*(?:/\\.\\.\\.)?)?|[A-Za-z0-9][A-Za-z0-9._-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)+(?:/\\.\\.\\.)?)$"}}}`)

type Authority struct {
	WorkspaceRoot      string
	Permissions        []string
	Purpose            kernel.WorkPurpose
	EffectPolicyDigest kernel.Digest
}

type Call struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Result struct {
	Changes         []FileChangeSummary `json:"changes,omitempty"`
	Definitions     []SymbolLocation    `json:"definitions,omitempty"`
	References      []SymbolLocation    `json:"references,omitempty"`
	Unresolved      []string            `json:"unresolved,omitempty"`
	ChangeID        string              `json:"change_id,omitempty"`
	Removed         bool                `json:"removed,omitempty"`
	Name            string              `json:"name"`
	Path            string              `json:"path,omitempty"`
	Output          string              `json:"output,omitempty"`
	Files           []string            `json:"files,omitempty"`
	Matches         []SearchMatch       `json:"matches,omitempty"`
	MatchCount      *int                `json:"match_count,omitempty"`
	Ignored         *bool               `json:"ignored,omitempty"`
	IndexTree       string              `json:"index_tree,omitempty"`
	CommitSHA       string              `json:"commit_sha,omitempty"`
	Truncated       bool                `json:"truncated,omitempty"`
	SkippedFiles    int                 `json:"skipped_files,omitempty"`
	NextAfter       string              `json:"next_after,omitempty"`
	HEAD            string              `json:"head,omitempty"`
	Branch          string              `json:"branch,omitempty"`
	SHA256          string              `json:"sha256,omitempty"`
	SnapshotSHA256  string              `json:"snapshot_sha256,omitempty"`
	LineCount       *int                `json:"line_count,omitempty"`
	Status          string              `json:"status,omitempty"`
	StartLine       *int                `json:"start_line,omitempty"`
	EndLine         *int                `json:"end_line,omitempty"`
	NextStartLine   *int                `json:"next_start_line,omitempty"`
	ContentComplete *bool               `json:"content_complete,omitempty"`
	MaxReadLines    int                 `json:"max_read_lines,omitempty"`
	FormatClean     *bool               `json:"format_clean,omitempty"`
	Clean           *bool               `json:"clean,omitempty"`
	ExitCode        int                 `json:"exit_code,omitempty"`
	// A known preparation/process failure is a completed receipt, not an
	// unknown effect. ExitCode -1 means no completed test-suite result.
	ExecutionError   string `json:"execution_error,omitempty"`
	PermissionDenial bool   `json:"permission_denial,omitempty"`
}

type Host struct {
	GitBinary        string
	GoBinary         string
	Timeout          time.Duration
	CompilationCache string
}

// Available exposes only tools permitted by the admitted role. repository.edit
// implies read authority, matching the current Teams execution profile.
func Available(permissions []string) []Spec {
	available := make([]Spec, 0, len(specs))
	for _, spec := range specs {
		if permitted(permissions, spec.Permission) {
			spec.InputSchema = bytes.Clone(spec.InputSchema)
			available = append(available, spec)
		}
	}
	return available
}

func permitted(permissions []string, required string) bool {
	if required == "test.execute" {
		return slices.Contains(permissions, "test.execute") && permitted(permissions, "repository.read")
	}
	return slices.Contains(permissions, required) || required == "repository.read" && slices.Contains(permissions, "repository.edit")
}

func (host Host) execute(ctx context.Context, authority Authority, call Call) (Result, error) {
	if call.Name == "write_file" {
		return Result{}, ErrForbidden
	}
	var spec *Spec
	for i := range specs {
		if specs[i].Name == call.Name {
			spec = &specs[i]
			break
		}
	}
	if spec == nil || len(call.Arguments) == 0 {
		return Result{}, ErrInvalidCall
	}
	if !permitted(authority.Permissions, spec.Permission) {
		return Result{}, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil || host.Timeout <= 0 {
		return Result{}, ErrInvalidCall
	}
	result := Result{Name: call.Name}
	switch call.Name {
	case "find_definitions", "find_references":
		return host.findSymbols(ctx, root, call)
	case "check_go_build_worktree":
		if decode(call.Arguments, &struct{}{}) != nil {
			return Result{}, ErrInvalidCall
		}
		result, err = host.runIsolatedGoTestsFrom(ctx, root, "./...", true, true)
		result.Name = call.Name
	case "read_file":
		var args struct {
			Path      string `json:"path"`
			StartLine *int   `json:"start_line"`
			LineCount *int   `json:"line_count"`
		}
		if decode(call.Arguments, &args) != nil || args.Path == "" {
			return Result{}, ErrInvalidCall
		}
		var supplied map[string]json.RawMessage
		_ = json.Unmarshal(call.Arguments, &supplied)
		for _, field := range []string{"start_line", "line_count"} {
			if bytes.Equal(bytes.TrimSpace(supplied[field]), []byte("null")) {
				return Result{}, ErrInvalidCall
			}
		}
		start, count := 1, maxReadFileLines
		if args.StartLine != nil {
			start = *args.StartLine
			if start < 1 {
				return Result{}, ErrInvalidCall
			}
		}
		if args.LineCount != nil {
			count = *args.LineCount
			if count < 1 || count > maxReadFileLines {
				return Result{}, fmt.Errorf("%w: read_file path=%q supports 1 through %d lines per call", ErrInvalidCall, args.Path, maxReadFileLines)
			}
		}
		var lineCount int
		result.Output, result.SHA256, lineCount, err = readFile(root, args.Path, start, count)
		if errors.Is(err, os.ErrNotExist) {
			// Only an absent, confined path is a discovery miss. Permission,
			// symlink and traversal failures remain explicit errors.
			if boundaryErr := checkMissingFilePath(root, args.Path); boundaryErr != nil {
				return Result{}, boundaryErr
			}
			return Result{Name: call.Name, Path: args.Path, Status: "NOT_FOUND"}, nil
		}
		if err == nil {
			result.Status = "FOUND"
			result.LineCount = &lineCount
			result.Path = args.Path
			actualStart, actualEnd := start, 0
			if lineCount == 0 || actualStart > lineCount {
				actualStart, actualEnd = 0, 0
			} else {
				actualEnd = actualStart + min(count, lineCount-actualStart+1) - 1
				if actualEnd < lineCount {
					next := actualEnd + 1
					result.NextStartLine = &next
				}
			}
			complete := lineCount == 0 || actualStart == 1 && actualEnd == lineCount
			result.StartLine, result.EndLine = &actualStart, &actualEnd
			result.ContentComplete, result.Truncated = &complete, !complete
			result.MaxReadLines = maxReadFileLines
		}
	case "check_go_format":
		var args struct {
			Paths []string `json:"paths"`
		}
		if decode(call.Arguments, &args) != nil || len(args.Paths) < 1 || len(args.Paths) > 32 {
			return Result{}, ErrInvalidCall
		}
		result.Files, err = checkGoFormat(root, args.Paths)
		if err == nil {
			clean := len(result.Files) == 0
			result.FormatClean = &clean
		}
	case "list_files":
		var args struct {
			Path       string `json:"path"`
			StartAfter string `json:"start_after"`
			Limit      *int   `json:"limit"`
		}
		if decode(call.Arguments, &args) != nil || args.Limit != nil && (*args.Limit < 1 || *args.Limit > 200) {
			return Result{}, ErrInvalidCall
		}
		limit := 200
		if args.Limit != nil {
			limit = *args.Limit
		}
		result.Files, result.Truncated, result.NextAfter, err = listFiles(root, args.Path, args.StartAfter, limit)
	case "find_files":
		var args struct {
			FilenameGlob string `json:"filename_glob"`
			Directory    string `json:"directory"`
			StartAfter   string `json:"start_after"`
			Limit        *int   `json:"limit"`
			Summary      string `json:"summary"`
		}
		if decode(call.Arguments, &args) != nil || args.Limit != nil && (*args.Limit < 1 || *args.Limit > 200) {
			return Result{}, ErrInvalidCall
		}
		limit := 200
		if args.Limit != nil {
			limit = *args.Limit
		}
		result.Files, result.Truncated, result.NextAfter, err = findFiles(ctx, root, args.Directory, args.FilenameGlob, args.StartAfter, limit)
	case "search_text", "search_regex":
		var args struct {
			Regex        string `json:"regex"`
			Text         string `json:"text"`
			Path         string `json:"path"`
			FilenameGlob string `json:"filename_glob"`
			ExcludeGlob  string `json:"exclude_glob"`
			IgnoreCase   bool   `json:"ignore_case"`
			ResultMode   string `json:"result_mode"`
			AfterLines   *int   `json:"after_lines"`
			MaxResults   *int   `json:"max_results"`
			Summary      string `json:"summary"`
		}
		if decode(call.Arguments, &args) != nil || args.MaxResults != nil && (*args.MaxResults < 1 || *args.MaxResults > 100) || args.AfterLines != nil && (*args.AfterLines < 0 || *args.AfterLines > 40) {
			return Result{}, ErrInvalidCall
		}
		// Match the two discrete schemas even for direct host callers.
		var fields map[string]json.RawMessage
		if json.Unmarshal(call.Arguments, &fields) != nil {
			return Result{}, ErrInvalidCall
		}
		if call.Name == "search_text" && (args.Text == "" || fields["regex"] != nil) || call.Name == "search_regex" && (args.Regex == "" || fields["text"] != nil) {
			return Result{}, ErrInvalidCall
		}
		limit := 100
		if args.MaxResults != nil {
			limit = *args.MaxResults
		}
		after := 0
		if args.AfterLines != nil {
			after = *args.AfterLines
		}
		var searchResult contentSearchResult
		searchResult, err = searchFileContents(ctx, root, contentSearchQuery{Path: args.Path, FilenameGlob: args.FilenameGlob, ExcludeGlob: args.ExcludeGlob, Regex: args.Regex, Literal: args.Text, IgnoreCase: args.IgnoreCase, ResultMode: args.ResultMode, AfterLines: after, MaxResults: limit})
		result.Matches, result.Files, result.MatchCount, result.Truncated, result.SkippedFiles = searchResult.Matches, searchResult.Files, searchResult.MatchCount, searchResult.Truncated, searchResult.SkippedFiles
	case "git_status":
		var args struct{}
		if decode(call.Arguments, &args) != nil {
			return Result{}, ErrInvalidCall
		}
		if err := host.requireGitRoot(ctx, root); err != nil {
			return Result{}, err
		}
		result, err = host.command(ctx, root, call.Name, host.gitBinary(), []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.interactive=never", "status", "--porcelain=v1", "--untracked-files=normal"})
		if err == nil && result.ExitCode == 0 {
			clean := strings.TrimSpace(result.Output) == ""
			result.Clean = &clean
			var head, branch Result
			head, err = host.command(ctx, root, call.Name, host.gitBinary(), []string{"rev-parse", "HEAD"})
			if err == nil {
				branch, err = host.command(ctx, root, call.Name, host.gitBinary(), []string{"branch", "--show-current"})
			}
			if err == nil && branch.ExitCode == 0 {
				result.Branch = strings.TrimSpace(branch.Output)
				if head.ExitCode == 0 {
					result.HEAD = strings.TrimSpace(head.Output)
				} else {
					result.HEAD = "UNBORN"
				}
			} else if err == nil {
				err = ErrInvalidCall
			}
		}
	case "git_diff":
		var args struct {
			Path       string `json:"path"`
			FromCommit string `json:"from_commit"`
			ToCommit   string `json:"to_commit"`
			Staged     bool   `json:"staged"`
			Format     string `json:"format"`
		}
		if decode(call.Arguments, &args) != nil || args.ToCommit != "" && args.FromCommit == "" || args.Staged && (args.FromCommit != "" || args.ToCommit != "") ||
			args.FromCommit != "" && !validGitRevision(args.FromCommit) || args.ToCommit != "" && !validGitRevision(args.ToCommit) {
			return Result{}, ErrInvalidCall
		}
		if err := host.requireGitRoot(ctx, root); err != nil {
			return Result{}, err
		}
		arguments := []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.interactive=never", "diff", "--no-ext-diff", "--no-textconv"}
		if args.Staged {
			arguments = append(arguments, "--cached")
		}
		switch args.Format {
		case "", "patch":
		case "stat":
			arguments = append(arguments, "--stat")
		case "name_status":
			arguments = append(arguments, "--name-status")
		case "numstat":
			arguments = append(arguments, "--numstat")
		case "name_only":
			arguments = append(arguments, "--name-only")
		default:
			return Result{}, ErrInvalidCall
		}
		if args.FromCommit != "" {
			arguments = append(arguments, args.FromCommit)
		}
		if args.ToCommit != "" {
			arguments = append(arguments, args.ToCommit)
		}
		if args.Path != "" {
			if _, err = relativePath(args.Path); err != nil {
				return Result{}, err
			}
			arguments = append(arguments, "--", ":(literal)"+args.Path)
		}
		result, err = host.command(ctx, root, call.Name, host.gitBinary(), arguments)
	case "git_log":
		var args struct {
			Limit  *int   `json:"limit"`
			Format string `json:"format"`
		}
		if decode(call.Arguments, &args) != nil || args.Limit != nil && (*args.Limit < 1 || *args.Limit > 20) {
			return Result{}, ErrInvalidCall
		}
		if err := host.requireGitRoot(ctx, root); err != nil {
			return Result{}, err
		}
		limit := 5
		if args.Limit != nil {
			limit = *args.Limit
		}
		format := "--format=%h %s"
		switch args.Format {
		case "", "oneline":
		case "message":
			format = "--format=%B"
		default:
			return Result{}, ErrInvalidCall
		}
		result, err = host.command(ctx, root, call.Name, host.gitBinary(), []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.interactive=never", "log", "-n", fmt.Sprint(limit), format})
	case "git_show":
		var args struct {
			Commit string `json:"commit"`
			Path   string `json:"path"`
			Format string `json:"format"`
		}
		if decode(call.Arguments, &args) != nil || !validGitRevision(args.Commit) {
			return Result{}, ErrInvalidCall
		}
		if err := host.requireGitRoot(ctx, root); err != nil {
			return Result{}, err
		}
		arguments := []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.interactive=never", "show", "--no-ext-diff", "--no-textconv"}
		switch args.Format {
		case "", "patch":
		case "stat":
			arguments = append(arguments, "--stat")
		default:
			return Result{}, ErrInvalidCall
		}
		arguments = append(arguments, args.Commit)
		if args.Path != "" {
			if _, err = relativePath(args.Path); err != nil {
				return Result{}, err
			}
			arguments = append(arguments, "--", ":(literal)"+args.Path)
		}
		result, err = host.command(ctx, root, call.Name, host.gitBinary(), arguments)
	case "git_check_ignore":
		var args struct {
			Path string `json:"path"`
		}
		if decode(call.Arguments, &args) != nil {
			return Result{}, ErrInvalidCall
		}
		if _, err = relativePath(strings.TrimSuffix(args.Path, string(filepath.Separator))); err != nil {
			return Result{}, err
		}
		if err := host.requireGitRoot(ctx, root); err != nil {
			return Result{}, err
		}
		result, err = host.command(ctx, root, call.Name, host.gitBinary(), []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.interactive=never", "check-ignore", "-q", "--", args.Path})
		if err == nil {
			if result.ExitCode > 1 {
				err = ErrInvalidCall
			} else {
				ignored := result.ExitCode == 0
				result.Ignored = &ignored
				result.ExitCode = 0
			}
		}
	case "git_stage_files":
		var args struct {
			Paths        []string `json:"paths"`
			ExpectedHead string   `json:"expected_head"`
		}
		if decode(call.Arguments, &args) != nil || !validFullGitSHA(args.ExpectedHead) || len(args.Paths) < 1 || len(args.Paths) > 32 {
			return Result{}, ErrInvalidCall
		}
		result, err = host.gitStageFiles(ctx, root, args.Paths, args.ExpectedHead)
	case "git_commit":
		var args struct {
			ExpectedHead      string `json:"expected_head"`
			ExpectedIndexTree string `json:"expected_index_tree"`
			Subject           string `json:"subject"`
			Body              string `json:"body"`
		}
		if decode(call.Arguments, &args) != nil || !validFullGitSHA(args.ExpectedHead) || !validFullGitSHA(args.ExpectedIndexTree) || !validCommitMessage(args.Subject, args.Body) {
			return Result{}, ErrInvalidCall
		}
		result, err = host.gitCommit(ctx, root, args.ExpectedHead, args.ExpectedIndexTree, args.Subject, args.Body)
	case "run_go_tests":
		var args struct {
			Package string `json:"package"`
		}
		if decode(call.Arguments, &args) != nil || !validGoPackageInModule(root, args.Package) {
			return Result{}, ErrInvalidCall
		}
		result, err = host.runIsolatedGoTests(ctx, root, args.Package)
	case "run_go_tests_worktree":
		var args struct {
			Package string `json:"package"`
		}
		if decode(call.Arguments, &args) != nil || !validGoPackageInModule(root, args.Package) {
			return Result{}, ErrInvalidCall
		}
		if authority.Purpose != kernel.PurposeImplementation && authority.Purpose != kernel.PurposeRepair {
			return Result{}, ErrForbidden
		}
		result, err = host.runIsolatedGoTestsWorktree(ctx, root, args.Package)
	case "create_file":
		var args createFileArgs
		if decode(call.Arguments, &args) != nil || args.Path == "" || len(args.Content) > 1<<20 || !utf8.ValidString(args.Content) {
			return Result{}, ErrInvalidCall
		}
		result.SHA256, err = createFile(root, args.Path, []byte(args.Content))
	case "read_file_from_commit":
		return host.readFileFromCommit(ctx, root, call)
	case "edit_file":
		args, decodeErr := decodeFileEdit(call.Arguments)
		if decodeErr != nil {
			return Result{}, decodeErr
		}
		content, prepareErr := prepareFileEdit(root, args)
		if prepareErr != nil {
			return Result{}, prepareErr
		}
		result.SHA256, err = writeFile(root, args.Path, content, args.ExpectedSHA256)
	default:
		return Result{}, ErrInvalidCall
	}
	return result, err
}

func decode(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return ErrInvalidCall
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrInvalidCall
	}
	return nil
}

func canonicalRoot(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrBoundary
	}
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", ErrBoundary
	}
	return root, nil
}

func relativePath(path string) (string, error) {
	if filepath.IsAbs(path) || path == "" || path == "." || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", ErrBoundary
	}
	return path, nil
}

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func existingPath(root, relative string) (string, error) {
	if relative == "" || relative == "." {
		return root, nil
	}
	if _, err := relativePath(relative); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	if !withinRoot(root, resolved) {
		return "", ErrBoundary
	}
	return resolved, nil
}

const maxReadFileLines = 400

func readFile(root, relative string, start, count int) (string, string, int, error) {
	if start < 1 || count < 1 || count > maxReadFileLines {
		return "", "", 0, ErrInvalidCall
	}
	path, err := existingPath(root, relative)
	if err != nil {
		return "", "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", 0, ErrInvalidCall
	}
	if info.Size() > 1<<20 {
		return "", "", 0, ErrTooLarge
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, err
	}
	if !utf8.Valid(content) {
		return "", "", 0, ErrInvalidCall
	}
	fullHash := sha256.Sum256(content)
	if len(content) == 0 {
		return "", hex.EncodeToString(fullHash[:]), 0, nil
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	lineCount := len(lines)
	if start > lineCount {
		return "", hex.EncodeToString(fullHash[:]), lineCount, nil
	}
	// Subtract before adding: an arbitrarily large start must not overflow.
	end := start + min(count, lineCount-start+1) - 1
	selected := strings.Join(lines[start-1:end], "\n")
	if len(selected) > 64<<10 {
		return "", "", 0, ErrTooLarge
	}
	return selected, hex.EncodeToString(fullHash[:]), lineCount, nil
}

func checkGoFormat(root string, paths []string) ([]string, error) {
	unformatted := make([]string, 0)
	seen := make(map[string]bool, len(paths))
	for _, relative := range paths {
		if seen[relative] || filepath.Ext(relative) != ".go" {
			return nil, ErrInvalidCall
		}
		seen[relative] = true
		path, err := existingPath(root, relative)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, ErrInvalidCall
		}
		if info.Size() > 1<<20 {
			return nil, ErrTooLarge
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		formatted, err := format.Source(content)
		if err != nil {
			return nil, fmt.Errorf("gofmt %s: %w", relative, err)
		}
		if !bytes.Equal(content, formatted) {
			unformatted = append(unformatted, relative)
		}
	}
	return unformatted, nil
}

func listFiles(root, relative, startAfter string, limit int) ([]string, bool, string, error) {
	path, err := existingPath(root, relative)
	if err != nil {
		return nil, false, "", err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, false, "", err
	}
	files := make([]string, 0, min(limit, len(entries)))
	var lastName string
	for _, entry := range entries {
		name := entry.Name()
		if name <= startAfter {
			continue
		}
		if len(files) == limit {
			return files, true, lastName, nil
		}
		lastName = name
		if entry.IsDir() {
			name += "/"
		}
		files = append(files, name)
	}
	return files, false, "", nil
}

func validExpectedSHA(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func validGitRevision(value string) bool {
	if value == "HEAD" {
		return true
	}
	if len(value) < 7 || len(value) > 40 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func validFullGitSHA(value string) bool {
	return len(value) == 40 && validGitRevision(value)
}

func validCommitMessage(subject, body string) bool {
	return subject != "" && len(subject) <= 200 && !strings.ContainsAny(subject, "\r\n\x00") && len(body) <= 5000 && !strings.ContainsAny(body, "\r\x00") && !strings.HasSuffix(body, "\n")
}

func writeFile(root, relative string, content []byte, expectedSHA string) (string, error) {
	if _, err := relativePath(relative); err != nil {
		return "", err
	}
	parent, err := existingPath(root, filepath.Dir(relative))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", ErrBoundary
	}
	path := filepath.Join(parent, filepath.Base(relative))
	current, statErr := os.Lstat(path)
	mode := os.FileMode(0644)
	if statErr == nil {
		if !current.Mode().IsRegular() {
			return "", ErrBoundary
		}
		mode = current.Mode().Perm()
		if current.Size() > 1<<20 {
			return "", ErrTooLarge
		}
		old, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", readErr
		}
		actual := sha256.Sum256(old)
		if expectedSHA != hex.EncodeToString(actual[:]) {
			return "", filePreconditionError(relative, expectedSHA, hex.EncodeToString(actual[:]), true)
		}
	} else if errors.Is(statErr, os.ErrNotExist) {
		if expectedSHA != "" {
			return "", filePreconditionError(relative, expectedSHA, "", false)
		}
	} else {
		return "", statErr
	}
	file, err := os.CreateTemp(parent, ".tekroo-write-")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(content); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Chmod(mode); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	// An exclusive link publishes a newly created file atomically without
	// overwriting a file that appeared after the precondition check.
	if expectedSHA == "" {
		err = os.Link(temporary, path)
	} else {
		err = os.Rename(temporary, path)
	}
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func filePreconditionError(path, expected, current string, exists bool) error {
	return fmt.Errorf("%w: path=%q expected_sha256=%q current_sha256=%q exists=%t", ErrConflict, path, expected, current, exists)
}

func validGoPackage(value string) bool {
	if value == "." || value == "./..." || value == "./" {
		return true
	}
	if !strings.HasPrefix(value, "./") {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(value, "./"), "/..."), "/") {
		if part == "" || part[0] == '.' {
			return false
		}
		for _, char := range part {
			if !(char == '_' || char == '-' || char == '.' || char >= '0' && char <= '9' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z') {
				return false
			}
		}
	}
	return true
}

// Go also accepts a package's module import path. Admit only names that map
// back into this workspace's own module; never resolve an external package.
func validGoPackageInModule(root, value string) bool {
	if validGoPackage(value) {
		if strings.HasSuffix(value, "/...") && value != "./..." {
			return existingGoPackageDirectory(root, strings.TrimSuffix(strings.TrimPrefix(value, "./"), "/..."))
		}
		return true
	}
	content, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || len(content) > 1<<20 {
		return false
	}
	module := ""
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			module = fields[1]
			break
		}
	}
	if module == "" || value == module {
		return value == module && module != ""
	}
	if !strings.HasPrefix(value, module+"/") {
		return false
	}
	suffix := strings.TrimSuffix(strings.TrimPrefix(value, module+"/"), "/...")
	if value == module+"/..." {
		return true
	}
	if !validGoPackage("./" + suffix) {
		return false
	}
	return existingGoPackageDirectory(root, suffix)
}

func existingGoPackageDirectory(root, suffix string) bool {
	path, err := existingPath(root, suffix)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (host Host) gitBinary() string {
	if host.GitBinary != "" {
		return host.GitBinary
	}
	return "git"
}

func (host Host) goBinary() string {
	if host.GoBinary != "" {
		return host.GoBinary
	}
	return "go"
}

type boundedOutput struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (output *boundedOutput) Write(chunk []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if len(output.data)+len(chunk) > output.limit {
		return 0, ErrTooLarge
	}
	output.data = append(output.data, chunk...)
	return len(chunk), nil
}

func (host Host) command(ctx context.Context, root, name, binary string, arguments []string) (Result, error) {
	operation, cancel := context.WithTimeout(ctx, host.Timeout)
	defer cancel()
	command := exec.CommandContext(operation, binary, arguments...)
	command.Dir = root
	command.Env = toolEnvironment()
	output := &boundedOutput{limit: 128 << 10}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	result := Result{Name: name, Output: string(output.data)}
	if operation.Err() != nil {
		return result, operation.Err()
	}
	if errors.Is(err, ErrTooLarge) {
		return result, ErrTooLarge
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("agent tool process failed: %w", err)
	}
	return result, nil
}

// toolEnvironment drops inherited Git and Go behavior overrides before running
// an exact argv. This is not a filesystem sandbox: test code still requires a
// separately isolated process boundary before untrusted live use.
func toolEnvironment() []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "TMPDIR": true, "USER": true,
		"GOROOT": true, "GOPATH": true, "GOCACHE": true, "CGO_ENABLED": true,
		"SDKROOT": true, "MACOSX_DEPLOYMENT_TARGET": true,
	}
	environment := make([]string, 0, len(allowed)+8)
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found && allowed[key] {
			environment = append(environment, entry)
		}
	}
	return append(environment, "LC_ALL=C", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off")
}

func (host Host) requireGitRoot(ctx context.Context, root string) error {
	result, err := host.command(ctx, root, "git_root", host.gitBinary(), []string{"rev-parse", "--show-toplevel"})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return ErrInvalidCall
	}
	resolved, err := filepath.EvalSymlinks(strings.TrimSpace(result.Output))
	if err != nil || resolved != root {
		return ErrBoundary
	}
	return nil
}
