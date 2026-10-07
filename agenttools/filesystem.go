package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tekroo-ai/teams/kernel"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Filesystem intents retain both images before any physical effect. Recovery
// only attests an observed complete postimage; it never repeats an uncertain
// multi-file action. Partial effects remain explicit, not silently retried.
type fileImage struct {
	Exists  bool   `json:"exists"`
	Content []byte `json:"content,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
}
type fileTransition struct {
	Path   string    `json:"path"`
	Before fileImage `json:"before"`
	After  fileImage `json:"after"`
}
type filesystemIntent struct {
	Files       []fileTransition `json:"files,omitempty"`
	Directories []string         `json:"directories,omitempty"`
}

func isFilesystemTool(name string) bool {
	return name == "make_directory" || name == "format_go_files" || name == "move_file" || name == "remove_file"
}

func safeMutationPath(path string) error {
	if _, err := relativePath(path); err != nil {
		return err
	}
	for _, reserved := range []string{".git", ".openhands"} {
		if path == reserved || strings.HasPrefix(path, reserved+"/") {
			return ErrForbidden
		}
	}
	return nil
}

func inspectFileImage(root, path string) (fileImage, error) {
	if err := validateChangePath(root, path); err != nil {
		return fileImage{}, err
	}
	resolved := filepath.Join(root, path)
	info, err := os.Lstat(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return fileImage{}, nil
	}
	if err != nil {
		return fileImage{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return fileImage{}, ErrBoundary
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return fileImage{}, err
	}
	return fileImage{Exists: true, Content: content, SHA256: contentSHA(content), Mode: uint32(info.Mode().Perm())}, nil
}

func sameImage(left, right fileImage) bool {
	return left.Exists == right.Exists && (!left.Exists || left.SHA256 == right.SHA256 && left.Mode == right.Mode)
}

func prepareFilesystem(root string, call Call) (filesystemIntent, error) {
	var intent filesystemIntent
	switch call.Name {
	case "make_directory":
		var args struct {
			Path string `json:"path"`
		}
		if decode(call.Arguments, &args) != nil || args.Path == "" || len(args.Path) > 1024 {
			return intent, ErrInvalidCall
		}
		if err := safeMutationPath(args.Path); err != nil {
			return intent, err
		}
		current := root
		for _, component := range strings.Split(args.Path, "/") {
			current = filepath.Join(current, component)
			info, err := os.Lstat(current)
			if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				return intent, ErrBoundary
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return intent, err
			}
			// Record all components, including pre-existing parents. This also
			// validates their type again during replay; none is ever removed.
			rel, _ := filepath.Rel(root, current)
			intent.Directories = append(intent.Directories, rel)
		}
	case "format_go_files":
		var args struct {
			Files []struct {
				Path           string `json:"path"`
				ExpectedSHA256 string `json:"expected_sha256"`
			} `json:"files"`
		}
		if decode(call.Arguments, &args) != nil || len(args.Files) < 1 || len(args.Files) > 16 {
			return intent, ErrInvalidCall
		}
		seen := map[string]bool{}
		for _, f := range args.Files {
			if seen[f.Path] || !strings.HasSuffix(f.Path, ".go") || len(f.ExpectedSHA256) != 64 || !validExpectedSHA(f.ExpectedSHA256) {
				return intent, ErrInvalidCall
			}
			seen[f.Path] = true
			before, err := inspectFileImage(root, f.Path)
			if err != nil {
				return intent, err
			}
			if !before.Exists || before.SHA256 != f.ExpectedSHA256 {
				return intent, filePreconditionError(f.Path, f.ExpectedSHA256, before.SHA256, before.Exists)
			}
			formatted, err := format.Source(before.Content)
			if err != nil {
				return intent, fmt.Errorf("format_go_files %s: %w", f.Path, err)
			}
			after := before
			after.Content = formatted
			after.SHA256 = contentSHA(formatted)
			intent.Files = append(intent.Files, fileTransition{f.Path, before, after})
		}
	case "move_file":
		var args struct {
			Source         string `json:"source"`
			Destination    string `json:"destination"`
			ExpectedSHA256 string `json:"expected_sha256"`
		}
		if decode(call.Arguments, &args) != nil || args.Source == args.Destination || len(args.ExpectedSHA256) != 64 || !validExpectedSHA(args.ExpectedSHA256) {
			return intent, ErrInvalidCall
		}
		before, err := inspectFileImage(root, args.Source)
		if err != nil {
			return intent, err
		}
		if !before.Exists || before.SHA256 != args.ExpectedSHA256 {
			return intent, filePreconditionError(args.Source, args.ExpectedSHA256, before.SHA256, before.Exists)
		}
		destination, err := inspectFileImage(root, args.Destination)
		if err != nil {
			return intent, err
		}
		if destination.Exists {
			return intent, fmt.Errorf("%w: destination %q already exists", ErrConflict, args.Destination)
		}
		intent.Files = []fileTransition{{args.Destination, destination, before}, {args.Source, before, fileImage{}}}
	case "remove_file":
		var args struct {
			Path           string `json:"path"`
			ExpectedSHA256 string `json:"expected_sha256"`
		}
		if decode(call.Arguments, &args) != nil || len(args.ExpectedSHA256) != 64 || !validExpectedSHA(args.ExpectedSHA256) {
			return intent, ErrInvalidCall
		}
		before, err := inspectFileImage(root, args.Path)
		if err != nil {
			return intent, err
		}
		if !before.Exists || before.SHA256 != args.ExpectedSHA256 {
			return intent, filePreconditionError(args.Path, args.ExpectedSHA256, before.SHA256, before.Exists)
		}
		intent.Files = []fileTransition{{args.Path, before, fileImage{}}}
	default:
		return intent, ErrForbidden
	}
	encoded, err := json.Marshal(intent)
	if err != nil || len(encoded) > 4<<20 {
		return filesystemIntent{}, ErrTooLarge
	}
	return intent, nil
}

// ValidateFilesystemSnapshot is shared with durable storage so corrupt images
// cannot acquire authority when rehydrated after a process restart.
func ValidateFilesystemSnapshot(encoded json.RawMessage) error {
	var intent filesystemIntent
	if len(encoded) == 0 || len(encoded) > 4<<20 || decode(encoded, &intent) != nil || (len(intent.Files) == 0) == (len(intent.Directories) == 0) || len(intent.Files) > 16 || len(intent.Directories) > 128 {
		return ErrInvalidCall
	}
	seen := map[string]bool{}
	for _, f := range intent.Files {
		if safeMutationPath(f.Path) != nil || seen[f.Path] {
			return ErrInvalidCall
		}
		seen[f.Path] = true
		for _, image := range []fileImage{f.Before, f.After} {
			if image.Exists {
				if len(image.Content) > 1<<20 || contentSHA(image.Content) != image.SHA256 || image.Mode > 0777 {
					return ErrInvalidCall
				}
			} else if len(image.Content) != 0 || image.SHA256 != "" || image.Mode != 0 {
				return ErrInvalidCall
			}
		}
	}
	for _, path := range intent.Directories {
		if safeMutationPath(path) != nil || seen[path] {
			return ErrInvalidCall
		}
		seen[path] = true
	}
	return nil
}

func filesystemPostimage(root string, intent filesystemIntent) (bool, error) {
	for _, f := range intent.Files {
		current, err := inspectFileImage(root, f.Path)
		if err != nil {
			return false, err
		}
		if !sameImage(current, f.After) {
			return false, nil
		}
	}
	for _, path := range intent.Directories {
		info, err := os.Lstat(filepath.Join(root, path))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, ErrBoundary
		}
	}
	return true, nil
}

func (gateway MutationGateway) executeFilesystem(ctx context.Context, request Request, reconcile bool) (Receipt, error) {
	if gateway.Bindings == nil || gateway.Ledger == nil || gateway.Host.Timeout <= 0 || !gateway.Policy.Valid() || !request.InvocationID.Valid() || !request.RequestDigest.Valid() || strings.TrimSpace(request.ToolCallID) == "" || len(request.ToolCallID) > 256 {
		return Receipt{}, ErrInvalidCall
	}
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	authority, err := gateway.Bindings.BindToolInvocation(operation, request.InvocationID, request.RequestDigest)
	if err != nil {
		return Receipt{}, err
	}
	if authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "repository.edit") || authority.Purpose != kernel.PurposeImplementation && authority.Purpose != kernel.PurposeRepair {
		return Receipt{}, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Receipt{}, err
	}
	unlock, err := lockEffectPath(operation, root, "")
	if err != nil {
		return Receipt{}, err
	}
	defer unlock()
	record := EffectRecord{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID, Name: request.Call.Name, ArgumentsSHA256: contentSHA(request.Call.Arguments), WorkspaceRoot: root, EffectPolicyHash: gateway.Policy}
	stored, found, err := gateway.Ledger.Lookup(operation, record)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if found {
		return gateway.reconcileFilesystem(operation, request, stored)
	}
	if reconcile {
		return Receipt{}, nil
	}
	intent, err := prepareFilesystem(root, request.Call)
	if err != nil {
		return Receipt{}, err
	}
	record.Filesystem, _ = json.Marshal(intent)
	record.PreparedSHA256 = contentSHA(record.Filesystem)
	if ValidateFilesystemSnapshot(record.Filesystem) != nil {
		return Receipt{}, ErrInvalidCall
	}
	stored, created, err := gateway.Ledger.Reserve(operation, record)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if !created {
		return gateway.reconcileFilesystem(operation, request, stored)
	}
	return gateway.applyFilesystem(operation, request, record, intent)
}

func (gateway MutationGateway) applyFilesystem(operation context.Context, request Request, record EffectRecord, intent filesystemIntent) (Receipt, error) {
	root := record.WorkspaceRoot
	for _, path := range intent.Directories {
		absolute := filepath.Join(root, path)
		if err := os.Mkdir(absolute, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return Receipt{}, errors.Join(ErrEffectUncertain, err)
		}
		info, err := os.Lstat(absolute)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Receipt{}, ErrEffectUncertain
		}
	}
	for _, f := range intent.Files {
		current, err := inspectFileImage(root, f.Path)
		if err != nil || !sameImage(current, f.Before) {
			return Receipt{}, ErrEffectUncertain
		}
		if sameImage(f.Before, f.After) {
			continue
		}
		if !f.After.Exists {
			err = os.Remove(filepath.Join(root, f.Path))
		} else {
			_, err = writeFile(root, f.Path, f.After.Content, f.Before.SHA256)
			if err == nil {
				err = os.Chmod(filepath.Join(root, f.Path), os.FileMode(f.After.Mode))
			}
		}
		if err != nil {
			return Receipt{}, errors.Join(ErrEffectUncertain, err)
		}
	}
	completion, stop := context.WithTimeout(context.Background(), gateway.Host.Timeout)
	defer stop()
	return gateway.reconcileFilesystem(completion, request, record)
}

func (gateway MutationGateway) undoFilesystem(ctx context.Context, request Request, record, prior EffectRecord, expected string) (Receipt, error) {
	if ValidateFilesystemSnapshot(prior.Filesystem) != nil || prior.PreparedSHA256 != contentSHA(prior.Filesystem) {
		return Receipt{}, ErrEffectUncertain
	}
	var completed Result
	if json.Unmarshal(prior.Result, &completed) != nil || completed.Name != prior.Name || completed.ExecutionError != "" || completed.ChangeID != fileChangeID(prior) || completed.SHA256 != prior.PreparedSHA256 {
		return Receipt{}, ErrEffectUncertain
	}
	if prior.PreparedSHA256 != expected {
		return Receipt{}, fmt.Errorf("%w: change_id=%s recorded_postcondition_sha256=%s supplied_sha256=%s; use file_change_history for currently applicable undo", ErrConflict, fileChangeID(prior), prior.PreparedSHA256, expected)
	}
	var original filesystemIntent
	_ = json.Unmarshal(prior.Filesystem, &original)
	if len(original.Files) == 0 {
		return Receipt{}, ErrForbidden
	}
	matched, err := filesystemPostimage(prior.WorkspaceRoot, original)
	if err != nil || !matched {
		return Receipt{}, errors.Join(ErrConflict, err)
	}
	var reversed filesystemIntent
	for i := len(original.Files) - 1; i >= 0; i-- {
		f := original.Files[i]
		reversed.Files = append(reversed.Files, fileTransition{f.Path, f.After, f.Before})
	}
	record.Filesystem, _ = json.Marshal(reversed)
	record.PreparedSHA256 = contentSHA(record.Filesystem)
	stored, created, err := gateway.Ledger.Reserve(ctx, record)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if !created {
		return gateway.reconcileFilesystem(ctx, request, stored)
	}
	return gateway.applyFilesystem(ctx, request, record, reversed)
}

func (gateway MutationGateway) reconcileFilesystem(ctx context.Context, request Request, record EffectRecord) (Receipt, error) {
	if ValidateFilesystemSnapshot(record.Filesystem) != nil || record.PreparedSHA256 != contentSHA(record.Filesystem) {
		return Receipt{}, ErrEffectUncertain
	}
	if len(record.Result) > 0 {
		var result Result
		if json.Unmarshal(record.Result, &result) != nil || result.Name != request.Call.Name || result.SHA256 != record.PreparedSHA256 || (result.ChangeID != "" && result.ChangeID != fileChangeID(record)) {
			return Receipt{}, ErrEffectUncertain
		}
		return effectReceipt(request, record.ArgumentsSHA256, result)
	}
	var intent filesystemIntent
	_ = json.Unmarshal(record.Filesystem, &intent)
	matched, err := filesystemPostimage(record.WorkspaceRoot, intent)
	if err != nil || !matched {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	result := Result{Name: request.Call.Name, SHA256: record.PreparedSHA256}
	if len(intent.Files) > 0 {
		result.ChangeID = fileChangeID(record)
		for _, f := range intent.Files {
			result.Files = append(result.Files, f.Path)
		}
	} else {
		result.Files = slices.Clone(intent.Directories)
	}
	return completeEffect(gateway.Ledger, request, record, result)
}
