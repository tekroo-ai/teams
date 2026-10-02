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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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
	{"read_file", "Read bounded lines from one workspace file.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1}}}`), "repository.read"},
	{"list_files", "List one workspace directory without recursing. If truncated, continue with next_after as start_after.", json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"start_after":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200}}}`), "repository.read"},
	{"git_status", "Read the worktree's branch, HEAD, and Git status without changing it.", json.RawMessage(`{"type":"object","additionalProperties":false}`), "repository.read"},
	{"git_diff", "Read the unstaged Git diff, optionally for one path.", json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","minLength":1}}}`), "repository.read"},
	{"write_file", "Create or replace one workspace file only when its expected SHA-256 matches.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path","content","expected_sha256"],"properties":{"path":{"type":"string","minLength":1},"content":{"type":"string"},"expected_sha256":{"type":"string","pattern":"^$|^[0-9a-f]{64}$"}}}`), "repository.edit"},
	{"run_go_tests", "Run Go tests for one workspace package pattern, without shell expansion or network dependency downloads.", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["package"],"properties":{"package":{"type":"string","pattern":"^\\./(?:\\.\\.|[A-Za-z0-9_./-]+)$"}}}`), "test.execute"},
}

type Authority struct {
	WorkspaceRoot string
	Permissions   []string
}

type Call struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Result struct {
	Name      string   `json:"name"`
	Output    string   `json:"output,omitempty"`
	Files     []string `json:"files,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	NextAfter string   `json:"next_after,omitempty"`
	HEAD      string   `json:"head,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	SHA256    string   `json:"sha256,omitempty"`
	ExitCode  int      `json:"exit_code,omitempty"`
}

type Host struct {
	GitBinary string
	GoBinary  string
	Timeout   time.Duration
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
	case "read_file":
		var args struct {
			Path      string `json:"path"`
			StartLine *int   `json:"start_line"`
			EndLine   *int   `json:"end_line"`
		}
		if decode(call.Arguments, &args) != nil || args.Path == "" {
			return Result{}, ErrInvalidCall
		}
		start, end := 0, 0
		if args.StartLine != nil {
			start = *args.StartLine
			if start < 1 {
				return Result{}, ErrInvalidCall
			}
		}
		if args.EndLine != nil {
			end = *args.EndLine
			if end < 1 {
				return Result{}, ErrInvalidCall
			}
		}
		result.Output, err = readFile(root, args.Path, start, end)
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
			Path string `json:"path"`
		}
		if decode(call.Arguments, &args) != nil {
			return Result{}, ErrInvalidCall
		}
		if err := host.requireGitRoot(ctx, root); err != nil {
			return Result{}, err
		}
		arguments := []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.interactive=never", "diff", "--no-ext-diff", "--no-textconv"}
		if args.Path != "" {
			if _, err = relativePath(args.Path); err != nil {
				return Result{}, err
			}
			arguments = append(arguments, "--", ":(literal)"+args.Path)
		}
		result, err = host.command(ctx, root, call.Name, host.gitBinary(), arguments)
	case "run_go_tests":
		var args struct {
			Package string `json:"package"`
		}
		if decode(call.Arguments, &args) != nil || !validGoPackage(args.Package) {
			return Result{}, ErrInvalidCall
		}
		result, err = host.command(ctx, root, call.Name, host.goBinary(), []string{"test", "-count=1", args.Package})
	case "write_file":
		var args struct {
			Path           string `json:"path"`
			Content        string `json:"content"`
			ExpectedSHA256 string `json:"expected_sha256"`
		}
		if decode(call.Arguments, &args) != nil || args.Path == "" || len(args.Content) > 1<<20 || !utf8.ValidString(args.Content) || !validExpectedSHA(args.ExpectedSHA256) {
			return Result{}, ErrInvalidCall
		}
		result.SHA256, err = writeFile(root, args.Path, []byte(args.Content), args.ExpectedSHA256)
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

func readFile(root, relative string, start, end int) (string, error) {
	path, err := existingPath(root, relative)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrInvalidCall
	}
	if info.Size() > 1<<20 {
		return "", ErrTooLarge
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(content) {
		return "", ErrInvalidCall
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if start == 0 {
		start = 1
	}
	if end == 0 {
		end = min(len(lines), start+399)
	}
	if start < 1 || end < start || end-start >= 400 || start > len(lines) || end > len(lines) {
		return "", ErrInvalidCall
	}
	selected := strings.Join(lines[start-1:end], "\n")
	if len(selected) > 64<<10 {
		return "", ErrTooLarge
	}
	return selected, nil
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
			return "", ErrConflict
		}
	} else if errors.Is(statErr, os.ErrNotExist) {
		if expectedSHA != "" {
			return "", ErrConflict
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
	if err = os.Rename(temporary, path); err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func validGoPackage(value string) bool {
	if value == "./..." {
		return true
	}
	if !strings.HasPrefix(value, "./") || value == "./" {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(value, "./"), "/") {
		if part == "" || part == "." || part == ".." {
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
	if err != nil || result.ExitCode != 0 {
		return ErrInvalidCall
	}
	resolved, err := filepath.EvalSymlinks(strings.TrimSpace(result.Output))
	if err != nil || resolved != root {
		return ErrBoundary
	}
	return nil
}
