package openhands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestEventsAtLeafReusesOnlyExactJournalVersion(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"items":[],"next_page_id":""}`))
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		baseURL: base, sessionAPIKey: "test", http: server.Client(), maximumPages: 1,
		maximumEvidenceBytes: 1024, eventsCache: make(map[string]cachedConversationEvents),
	}
	if _, err := client.eventsAtLeaf(context.Background(), "conversation", "leaf-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.eventsAtLeaf(context.Background(), "conversation", "leaf-1"); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("unchanged leaf made %d journal requests, want 1", got)
	}
	if _, err := client.eventsAtLeaf(context.Background(), "conversation", "leaf-2"); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("changed leaf made %d total journal requests, want 2", got)
	}
}
