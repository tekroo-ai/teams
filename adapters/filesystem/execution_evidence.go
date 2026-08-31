package filesystem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var ErrEvidenceBlobConflict = errors.New("execution evidence blob conflicts with retained content")

type ExecutionEvidenceStore struct {
	root string
}

func NewExecutionEvidenceStore(root string) (*ExecutionEvidenceStore, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, application.ErrInvalidConfiguration
	}
	return &ExecutionEvidenceStore{root: filepath.Clean(root)}, nil
}

func (store *ExecutionEvidenceStore) Put(ctx context.Context, evidenceID kernel.UUIDv7, content []byte) (application.ExecutionEvidenceBlobReceipt, error) {
	if err := ctx.Err(); err != nil {
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	if !evidenceID.Valid() || len(content) > 16<<20 {
		return application.ExecutionEvidenceBlobReceipt{}, application.ErrInvalidOperationalExecution
	}
	digestBytes := sha256.Sum256(content)
	digest := hex.EncodeToString(digestBytes[:])
	directory := filepath.Join(store.root, digest[:2])
	path := filepath.Join(directory, digest)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	if retained, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(retained, content) {
			return application.ExecutionEvidenceBlobReceipt{}, ErrEvidenceBlobConflict
		}
		return blobReceipt(path, digest, len(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	temporary, err := os.CreateTemp(directory, ".execution-evidence-*")
	if err != nil {
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	if err := temporary.Close(); err != nil {
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if retained, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(retained, content) {
			return blobReceipt(path, digest, len(content)), nil
		}
		return application.ExecutionEvidenceBlobReceipt{}, err
	}
	removeTemporary = false
	return blobReceipt(path, digest, len(content)), nil
}

func (store *ExecutionEvidenceStore) Read(ctx context.Context, digest kernel.Digest) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !digest.Valid() {
		return nil, application.ErrInvalidOperationalExecution
	}
	path := filepath.Join(store.root, string(digest[:2]), string(digest))
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	observed := sha256.Sum256(content)
	if hex.EncodeToString(observed[:]) != string(digest) {
		return nil, ErrEvidenceBlobConflict
	}
	return content, nil
}

func blobReceipt(path, digest string, length int) application.ExecutionEvidenceBlobReceipt {
	return application.ExecutionEvidenceBlobReceipt{Locator: path, SHA256: kernel.Digest(digest), ByteLength: uint64(length)}
}

var _ application.ExecutionEvidenceBlobStore = (*ExecutionEvidenceStore)(nil)
