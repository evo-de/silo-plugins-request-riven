package riven

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
)

const unauthenticated = `{"data":null,"errors":[{"message":"Access denied! You need to be authenticated to perform this action!","extensions":{"code":"UNAUTHENTICATED"}}]}`

type gqlCall struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// gqlStub serves a fixed GraphQL response body for every /graphql POST and
// records the decoded request bodies it received.
type gqlStub struct {
	mu        sync.Mutex
	calls     []gqlCall
	responses []string // one response body per call, in order; last is reused once exhausted
}

func (s *gqlStub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Method != http.MethodPost {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		var call gqlCall
		_ = json.NewDecoder(r.Body).Decode(&call)

		s.mu.Lock()
		s.calls = append(s.calls, call)
		idx := len(s.calls) - 1
		s.mu.Unlock()

		if idx >= len(s.responses) {
			idx = len(s.responses) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.responses[idx]))
	})
}

func newStub(t *testing.T, responses ...string) (*gqlStub, *httpclient.Client) {
	t.Helper()
	stub := &gqlStub{responses: responses}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	return stub, NewClient(srv.URL, "k")
}

func TestNewClientSendsBearerToken(t *testing.T) {
	auth := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(srv.Close)

	if err := Ping(context.Background(), NewClient(srv.URL, "riven-key")); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := <-auth; got != "Bearer riven-key" {
		t.Fatalf("Authorization: want %q, got %q", "Bearer riven-key", got)
	}
}

func TestRequestItemSendsOnlyTheItemsID(t *testing.T) {
	cases := []struct {
		name      string
		item      Item
		wantInput map[string]any
	}{
		{"movie by TMDB", Item{Type: Movie, ID: "42"}, map[string]any{"type": "movie", "tmdbId": "42", "requestedBy": "silo"}},
		{"show by TVDB", Item{Type: Show, ID: "99"}, map[string]any{"type": "show", "tvdbId": "99", "requestedBy": "silo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub, client := newStub(t, `{"data":{"requestItem":true}}`)

			if err := RequestItem(context.Background(), client, tc.item, "silo"); err != nil {
				t.Fatalf("RequestItem: %v", err)
			}
			if len(stub.calls) != 1 {
				t.Fatalf("want 1 request, got %d", len(stub.calls))
			}
			call := stub.calls[0]
			if !strings.Contains(call.Query, "requestItem(") {
				t.Fatalf("want the requestItem mutation, got query: %s", call.Query)
			}
			if want := map[string]any{"input": tc.wantInput}; !reflect.DeepEqual(call.Variables, want) {
				t.Fatalf("variables: want %v, got %v", want, call.Variables)
			}
		})
	}
}

func TestRequestItemSurfacesGraphQLError(t *testing.T) {
	_, client := newStub(t, unauthenticated)

	err := RequestItem(context.Background(), client, Item{Type: Movie, ID: "42"}, "silo")
	if err == nil || !strings.Contains(err.Error(), "Access denied") {
		t.Fatalf("want riven's error message, got %v", err)
	}
}

func TestItemStatus(t *testing.T) {
	cases := []struct {
		name     string
		item     Item
		response string
		wantVars map[string]any
		want     ItemState
	}{
		{
			name:     "movie with a media item",
			item:     Item{Type: Movie, ID: "42"},
			response: `{"data":{"itemRequestByExternalIds":{"state":"completed","primaryMediaItem":{"state":"downloaded"}}}}`,
			wantVars: map[string]any{"tmdbId": "42"},
			want:     ItemState{Request: "completed", Media: "downloaded"},
		},
		{
			name:     "show before indexing",
			item:     Item{Type: Show, ID: "99"},
			response: `{"data":{"itemRequestByExternalIds":{"state":"requested","primaryMediaItem":null}}}`,
			wantVars: map[string]any{"tvdbId": "99"},
			want:     ItemState{Request: "requested"},
		},
		{
			name:     "no request",
			item:     Item{Type: Movie, ID: "42"},
			response: `{"data":{"itemRequestByExternalIds":null}}`,
			wantVars: map[string]any{"tmdbId": "42"},
			want:     ItemState{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub, client := newStub(t, tc.response)

			got, err := ItemStatus(context.Background(), client, tc.item)
			if err != nil {
				t.Fatalf("ItemStatus: %v", err)
			}
			if got != tc.want {
				t.Fatalf("state: want %+v, got %+v", tc.want, got)
			}
			call := stub.calls[0]
			if !strings.Contains(call.Query, "itemRequestByExternalIds(") {
				t.Fatalf("want the itemRequestByExternalIds query, got: %s", call.Query)
			}
			if !reflect.DeepEqual(call.Variables, tc.wantVars) {
				t.Fatalf("variables: want %v, got %v", tc.wantVars, call.Variables)
			}
		})
	}
}

func TestItemStatusSurfacesGraphQLError(t *testing.T) {
	_, client := newStub(t, unauthenticated)

	if _, err := ItemStatus(context.Background(), client, Item{Type: Show, ID: "99"}); err == nil {
		t.Fatal("want an error for an errors response")
	}
}

func TestPing(t *testing.T) {
	_, client := newStub(t, `{"data":{"instanceStatus":{"restartRequired":false}}}`)
	if err := Ping(context.Background(), client); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	_, client = newStub(t, unauthenticated)
	if err := Ping(context.Background(), client); err == nil {
		t.Fatal("want an error when riven rejects the key")
	}
}
