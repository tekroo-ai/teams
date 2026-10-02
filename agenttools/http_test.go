package agenttools

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type revocableBinding struct {
	authority Authority
	id        kernel.UUIDv7
	digest    kernel.Digest
	revoked   bool
}

func (binding *revocableBinding) BindToolInvocation(_ context.Context, id kernel.UUIDv7, digest kernel.Digest) (Authority, error) {
	if binding.revoked || id != binding.id || digest != binding.digest {
		return Authority{}, ErrStaleBinding
	}
	return binding.authority, nil
}

func TestReadOnlyHTTPServiceRejectsStaleOrMismatchedCredential(t *testing.T) {
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000951")
	digest := kernel.Digest(hex.EncodeToString(make([]byte, 32)))
	binding := &revocableBinding{id: id, digest: digest, authority: Authority{WorkspaceRoot: t.TempDir(), Permissions: []string{"repository.read"}}}
	service, err := NewHTTPService(Gateway{Bindings: binding, Host: Host{Timeout: time.Second}}, "http://127.0.0.1:8787", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.IssueReadOnlySession(context.Background(), id, digest)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, authorization string) int {
		recorder := httptest.NewRecorder()
		incoming := httptest.NewRequest(http.MethodGet, path, nil)
		if authorization != "" {
			incoming.Header.Set("Authorization", authorization)
		}
		service.Handler().ServeHTTP(recorder, incoming)
		return recorder.Code
	}
	if got := request(session.URL, ""); got != http.StatusUnauthorized {
		t.Fatalf("missing bearer status = %d", got)
	}
	if got := request(session.URL, "Bearer "+session.BearerToken); got == http.StatusUnauthorized || got == http.StatusForbidden {
		t.Fatalf("valid bearer was rejected: %d", got)
	}
	wrongDigest := strings.Repeat("a", 64)
	if got := request("http://127.0.0.1:8787"+ReadOnlyMCPPath+string(id)+"/"+wrongDigest, "Bearer "+session.BearerToken); got != http.StatusUnauthorized {
		t.Fatalf("bearer accepted for different digest: %d", got)
	}
	binding.revoked = true
	if got := request(session.URL, "Bearer "+session.BearerToken); got != http.StatusForbidden {
		t.Fatalf("revoked binding status = %d", got)
	}
	if _, err := service.IssueReadOnlySession(context.Background(), id, digest); err != ErrStaleBinding {
		t.Fatalf("issued new credential after revocation: %v", err)
	}
}
