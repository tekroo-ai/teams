package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/tekroo-ai/teams/kernel"
)

type FileChangeHistory interface {
	ListFileChanges(context.Context, string, kernel.Digest, string) ([]EffectRecord, error)
}

type FileChangeSummary struct {
	ChangeID            string `json:"change_id"`
	Tool                string `json:"tool"`
	PostconditionSHA256 string `json:"postcondition_sha256"`
	ApplicableUndo      bool   `json:"applicable_undo"`
}

func (gateway Gateway) fileHistory(ctx context.Context, authority Authority, call Call) (Result, error) {
	if gateway.History == nil || !permitted(authority.Permissions, "repository.read") {
		return Result{}, ErrForbidden
	}
	var args struct {
		Path string `json:"path"`
	}
	if decode(call.Arguments, &args) != nil || args.Path == "" {
		return Result{}, ErrInvalidCall
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Result{}, err
	}
	if err := validateChangePath(root, args.Path); err != nil {
		return Result{}, err
	}
	records, err := gateway.History.ListFileChanges(ctx, root, authority.EffectPolicyDigest, args.Path)
	if err != nil {
		return Result{}, err
	}
	if len(records) > 20 {
		return Result{}, ErrTooLarge
	}
	result := Result{Name: call.Name, Path: args.Path, Changes: []FileChangeSummary{}}
	for _, record := range records {
		if record.WorkspaceRoot != root || record.EffectPolicyHash != authority.EffectPolicyDigest {
			return Result{}, ErrForbidden
		}
		var output Result
		if json.Unmarshal(record.Result, &output) != nil {
			return Result{}, ErrEffectUncertain
		}
		if output.ExecutionError != "" || output.ChangeID == "" {
			continue
		}
		if output.ChangeID != fileChangeID(record) || output.Name != record.Name || output.SHA256 != record.PreparedSHA256 {
			return Result{}, ErrEffectUncertain
		}
		applicable := false
		if len(record.Filesystem) > 0 {
			if ValidateFilesystemSnapshot(record.Filesystem) != nil {
				return Result{}, ErrEffectUncertain
			}
			var intent filesystemIntent
			_ = json.Unmarshal(record.Filesystem, &intent)
			matchedPath := false
			for _, f := range intent.Files {
				if f.Path == args.Path {
					matchedPath = true
				}
			}
			if !matchedPath {
				return Result{}, ErrForbidden
			}
			applicable, err = filesystemPostimage(root, intent)
		} else {
			if record.FilePath != args.Path || !record.HasPreimage {
				return Result{}, ErrForbidden
			}
			current, exists, e := fileHash(root, args.Path)
			err = e
			applicable = !record.DesiredAbsent && exists && current == output.SHA256
		}
		if err != nil && !errors.Is(err, ErrBoundary) {
			return Result{}, err
		}
		result.Changes = append(result.Changes, FileChangeSummary{output.ChangeID, record.Name, output.SHA256, applicable && err == nil})
	}
	return result, nil
}
