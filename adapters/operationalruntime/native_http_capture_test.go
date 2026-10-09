//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Qualification receipts retain wire bodies (including reasoning) without
// credentials or headers. Recording failure fails qualification, never causes
// a second model request. Production traffic is not intercepted here.
type nativeHTTPCapture struct {
	base                http.RoundTripper
	directory, scenario string
	mu                  sync.Mutex
	sequence            int
}

func (c *nativeHTTPCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.sequence++
	n := c.sequence
	c.mu.Unlock()
	prefix := filepath.Join(c.directory, fmt.Sprintf("%s-http-%03d", c.scenario, n))
	started := time.Now().UTC()
	requestBody, err := io.ReadAll(io.LimitReader(req.Body, 16<<20+1))
	if err != nil {
		return nil, err
	}
	if len(requestBody) > 16<<20 {
		return nil, fmt.Errorf("qualification request capture exceeds 16 MiB")
	}
	if err := req.Body.Close(); err != nil {
		return nil, err
	}
	forward := req.Clone(req.Context())
	forward.Body = io.NopCloser(bytes.NewReader(requestBody))
	if err := captureWrite(prefix+"-request.json", requestBody); err != nil {
		return nil, err
	}
	base := c.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, requestErr := base.RoundTrip(forward)
	receipt := map[string]any{"started_at": started, "finished_at": time.Now().UTC(), "method": req.Method, "url": req.URL.Scheme + "://" + req.URL.Host + req.URL.Path, "request_sha256": digestBytes(requestBody)}
	if requestErr != nil {
		receipt["transport_error"] = requestErr.Error()
	} else {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(body) > 16<<20 {
			return nil, fmt.Errorf("qualification response capture exceeds 16 MiB")
		}
		if err := captureWrite(prefix+"-response.json", body); err != nil {
			return nil, err
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		receipt["status"] = response.StatusCode
		receipt["response_sha256"] = digestBytes(body)
		receipt["finished_at"] = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := captureWrite(prefix+"-receipt.json", append(raw, '\n')); err != nil {
		if response != nil {
			response.Body.Close()
		}
		return nil, err
	}
	return response, requestErr
}

func captureWrite(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func TestNativeHTTPCapturePreservesWireBodies(t *testing.T) {
	requestBody := []byte(`{"messages":[{"content":"exact input"}]}`)
	responseBody := []byte(`{"choices":[{"message":{"reasoning_content":"raw reasoning","content":"{}"}}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(got, requestBody) {
			t.Error("request changed", err)
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Write(responseBody)
	}))
	defer server.Close()
	dir := t.TempDir()
	req, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "never-record-this")
	response, err := (&http.Client{Transport: &nativeHTTPCapture{directory: dir, scenario: "probe"}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(got, responseBody) || response.StatusCode != http.StatusBadRequest {
		t.Fatal("response changed", err)
	}
	for suffix, want := range map[string][]byte{"request": requestBody, "response": responseBody} {
		got, err := os.ReadFile(filepath.Join(dir, "probe-http-001-"+suffix+".json"))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("receipt changed", suffix, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "probe-http-001-receipt.json"))
	if err != nil || bytes.Contains(raw, []byte("never-record-this")) {
		t.Fatal("credential leaked", err)
	}
	if err := captureWrite(filepath.Join(dir, "probe-http-001-request.json"), []byte("replacement")); err == nil {
		t.Fatal("receipt overwritten")
	}
}
