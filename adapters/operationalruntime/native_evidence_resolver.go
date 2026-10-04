package operationalruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tekroo-ai/teams/adapters/filesystem"
	"github.com/tekroo-ai/teams/kernel"
)

var errNativeEvidenceUnavailable = errors.New("admitted Teams evidence artifact is unavailable")

type nativeEvidenceRegistry interface {
	ReadAggregateRevisionHead(context.Context, kernel.AggregateRef) (uint64, kernel.UUIDv7, bool, error)
	ReadEvent(context.Context, kernel.UUIDv7) (kernel.DomainEvent, bool, error)
}

// Native evidence is either content-addressed execution output or an
// immutable Teams-produced artifact registered under the evidence root. The
// model supplies neither a filesystem path nor a digest.
type nativeEvidenceResolver struct {
	registry nativeEvidenceRegistry
	blobs    *filesystem.ExecutionEvidenceStore
	root     string
	byDigest map[kernel.Digest][]kernel.UUIDv7
}

func newNativeEvidenceResolver(registry nativeEvidenceRegistry, blobs *filesystem.ExecutionEvidenceStore, root string, refs []kernel.EvidenceRef) *nativeEvidenceResolver {
	resolver := &nativeEvidenceResolver{registry: registry, blobs: blobs, root: root, byDigest: make(map[kernel.Digest][]kernel.UUIDv7, len(refs))}
	for _, ref := range refs {
		resolver.byDigest[ref.SHA256] = append(resolver.byDigest[ref.SHA256], ref.EvidenceID)
	}
	return resolver
}

func (resolver *nativeEvidenceResolver) Read(ctx context.Context, digest kernel.Digest) ([]byte, error) {
	if resolver == nil || resolver.registry == nil || resolver.blobs == nil || !digest.Valid() || len(resolver.byDigest[digest]) == 0 {
		return nil, errNativeEvidenceUnavailable
	}
	if content, err := resolver.blobs.Read(ctx, digest); err == nil {
		return content, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, id := range resolver.byDigest[digest] {
		content, err := resolver.readRegisteredArtifact(ctx, id, digest)
		if err == nil {
			return content, nil
		}
		if !errors.Is(err, errNativeEvidenceUnavailable) {
			return nil, err
		}
	}
	return nil, errNativeEvidenceUnavailable
}

func (resolver *nativeEvidenceResolver) readRegisteredArtifact(ctx context.Context, id kernel.UUIDv7, digest kernel.Digest) ([]byte, error) {
	_, head, found, err := resolver.registry.ReadAggregateRevisionHead(ctx, kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: id})
	if err != nil || !found {
		return nil, errors.Join(errNativeEvidenceUnavailable, err)
	}
	event, found, err := resolver.registry.ReadEvent(ctx, head)
	if err != nil || !found || event.EventType != "tekroo.event.evidence.registered" || event.Aggregate != (kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: id}) {
		return nil, errors.Join(errNativeEvidenceUnavailable, err)
	}
	var record struct {
		Availability     string        `json:"availability"`
		IntegrityState   string        `json:"integrity_state"`
		LocatorImmutable bool          `json:"locator_immutable"`
		Locator          string        `json:"locator"`
		ByteLength       int64         `json:"byte_length"`
		SHA256           kernel.Digest `json:"sha256"`
	}
	if json.Unmarshal(event.Payload, &record) != nil || record.Availability != "AVAILABLE" || record.IntegrityState != "DIGEST_VERIFIED" || !record.LocatorImmutable || record.SHA256 != digest || record.ByteLength < 0 || record.ByteLength > 16<<20 {
		return nil, errNativeEvidenceUnavailable
	}
	root, rootErr := filepath.EvalSymlinks(resolver.root)
	path, pathErr := filepath.EvalSymlinks(record.Locator)
	if rootErr != nil || pathErr != nil || !filepath.IsAbs(record.Locator) {
		return nil, errNativeEvidenceUnavailable
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == "." {
		return nil, errNativeEvidenceUnavailable
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != record.ByteLength {
		return nil, errNativeEvidenceUnavailable
	}
	content, err := os.ReadFile(path)
	if err != nil || int64(len(content)) != record.ByteLength {
		return nil, errors.Join(errNativeEvidenceUnavailable, err)
	}
	sum := sha256.Sum256(content)
	if kernel.Digest(hex.EncodeToString(sum[:])) != digest {
		return nil, errNativeEvidenceUnavailable
	}
	return content, nil
}
