package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const unauthenticated = `{"data":null,"errors":[{"message":"Access denied! You need to be authenticated to perform this action!","extensions":{"code":"UNAUTHENTICATED"}}]}`

type gqlBody struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type fakeRiven struct {
	byQuerySubstring map[string]string
	received         []gqlBody
}

func (f *fakeRiven) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body gqlBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		f.received = append(f.received, body)
		for substr, resp := range f.byQuerySubstring {
			if strings.Contains(body.Query, substr) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
				return
			}
		}
		http.Error(w, "no stub matched query: "+body.Query, http.StatusNotFound)
	})
}

func startFakeRiven(t *testing.T, byQuerySubstring map[string]string) (*httptest.Server, *fakeRiven) {
	t.Helper()
	f := &fakeRiven{byQuerySubstring: byQuerySubstring}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return srv, f
}

func routerConn(id, baseURL string) *pluginv1.RouterConnection {
	return &pluginv1.RouterConnection{Id: id, BaseUrl: baseURL, ApiKey: "k"}
}

var requested = map[string]string{"requestItem": `{"data":{"requestItem":true}}`}

func TestFulfillRequestsItemByItsRivenID(t *testing.T) {
	cases := []struct {
		name      string
		request   *pluginv1.RequestDescriptor
		wantID    string
		wantInput map[string]any
	}{
		{
			name:      "movie by TMDB",
			request:   &pluginv1.RequestDescriptor{MediaType: "movie", Title: "Arrival", ExternalIds: map[string]string{"tmdb": "329865", "imdb": "tt2543164", "tvdb": "1"}},
			wantID:    "329865",
			wantInput: map[string]any{"type": "movie", "tmdbId": "329865", "requestedBy": "silo"},
		},
		{
			name:      "series by TVDB",
			request:   &pluginv1.RequestDescriptor{MediaType: "series", Title: "Severance", ExternalIds: map[string]string{"tvdb": "371980", "imdb": "tt11280740", "tmdb": "95396"}},
			wantID:    "371980",
			wantInput: map[string]any{"type": "show", "tvdbId": "371980", "requestedBy": "silo"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, f := startFakeRiven(t, requested)

			resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
				Request:     tc.request,
				Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
				Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
			})
			if err != nil {
				t.Fatalf("Fulfill: %v", err)
			}
			if len(resp.GetTargets()) != 1 {
				t.Fatalf("want 1 target, got %d msg=%q", len(resp.GetTargets()), resp.GetMessage())
			}
			tgt := resp.GetTargets()[0]
			if tgt.GetStatus() != "queued" || tgt.GetQuality() != "1080p" || tgt.GetExternalId() != tc.wantID ||
				tgt.GetExternalStatus() != "" || tgt.GetMessage() != "requested in riven" {
				t.Fatalf("bad target: %+v", tgt)
			}
			if len(f.received) != 1 {
				t.Fatalf("want 1 riven call, got %d", len(f.received))
			}
			if want := map[string]any{"input": tc.wantInput}; !reflect.DeepEqual(f.received[0].Variables, want) {
				t.Fatalf("riven received %v, want %v", f.received[0].Variables, want)
			}
		})
	}
}

// TestFulfillMultipleQualitiesShareOneRequest locks in the fix for a bug where
// only the first requested quality got a target: the host requires one target
// per requested quality, and a tier with none comes back gets marked failed
// even though riven (which has no per-quality profiles) is handling it fine.
func TestFulfillMultipleQualitiesShareOneRequest(t *testing.T) {
	srv, f := startFakeRiven(t, requested)

	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", Title: "Arrival", ExternalIds: map[string]string{"tmdb": "42"}},
		Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}, {Id: "2160p", Is4K: true}},
		Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 2 {
		t.Fatalf("want 2 targets (one per quality), got %d msg=%q", len(resp.GetTargets()), resp.GetMessage())
	}
	byQuality := map[string]*pluginv1.FulfillmentTarget{}
	for _, tgt := range resp.GetTargets() {
		byQuality[tgt.GetQuality()] = tgt
	}
	for _, q := range []string{"1080p", "2160p"} {
		tgt, ok := byQuality[q]
		if !ok {
			t.Fatalf("missing target for quality %q", q)
		}
		if tgt.GetStatus() != "queued" || tgt.GetExternalId() != "42" {
			t.Fatalf("bad %s target: %+v", q, tgt)
		}
	}
	if len(f.received) != 1 {
		t.Fatalf("want 1 riven call for 2 qualities on the same connection, got %d", len(f.received))
	}
}

func TestFulfillRefusesRequestWithoutRivenID(t *testing.T) {
	cases := []struct {
		name        string
		request     *pluginv1.RequestDescriptor
		wantMissing string
	}{
		{"series without TVDB", &pluginv1.RequestDescriptor{MediaType: "series", Title: "Severance", ExternalIds: map[string]string{"imdb": "tt11280740", "tmdb": "95396"}}, "TVDB"},
		{"movie without TMDB", &pluginv1.RequestDescriptor{MediaType: "movie", Title: "Arrival", ExternalIds: map[string]string{"imdb": "tt2543164", "tvdb": "1"}}, "TMDB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, f := startFakeRiven(t, requested)

			resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
				Request:     tc.request,
				Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
				Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
			})
			if err != nil {
				t.Fatalf("Fulfill: %v", err)
			}
			if len(resp.GetTargets()) != 0 || !strings.Contains(resp.GetMessage(), tc.wantMissing) {
				t.Fatalf("want no targets and a message naming %s, got %+v", tc.wantMissing, resp)
			}
			if len(f.received) != 0 {
				t.Fatalf("want no riven call, got %d", len(f.received))
			}
		})
	}
}

func TestFulfillPartialContainmentAcrossConnections(t *testing.T) {
	okSrv, _ := startFakeRiven(t, requested)
	errSrv, _ := startFakeRiven(t, map[string]string{"requestItem": unauthenticated})

	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request: &pluginv1.RequestDescriptor{MediaType: "movie", Title: "Arrival", ExternalIds: map[string]string{"tmdb": "42"}},
		Connections: []*pluginv1.RouterConnection{
			routerConn("good", okSrv.URL),
			routerConn("bad", errSrv.URL),
		},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(resp.GetTargets()) != 2 {
		t.Fatalf("want 2 targets, got %d", len(resp.GetTargets()))
	}
	byConn := map[string]*pluginv1.FulfillmentTarget{}
	for _, tgt := range resp.GetTargets() {
		byConn[tgt.GetConnectionId()] = tgt
	}
	if byConn["good"].GetStatus() != "queued" {
		t.Fatalf("good connection should be queued: %+v", byConn["good"])
	}
	if byConn["bad"].GetStatus() != "failed" || !strings.Contains(byConn["bad"].GetMessage(), "Access denied") {
		t.Fatalf("bad connection should be failed with riven's message: %+v", byConn["bad"])
	}
}

func TestCheckStatusMapsRivenState(t *testing.T) {
	cases := []struct {
		name               string
		request            string
		wantStatus, wantES string
	}{
		{"no request", `null`, "queued", ""},
		{"failed request", `{"state":"failed","primaryMediaItem":null}`, "failed", "failed"},
		{"failed request with a media item", `{"state":"failed","primaryMediaItem":{"state":"indexed"}}`, "failed", "indexed"},
		{"requested, not indexed", `{"state":"requested","primaryMediaItem":null}`, "queued", "requested"},
		{"completed", `{"state":"completed","primaryMediaItem":{"state":"completed"}}`, "completed", "completed"},
		{"media failed", `{"state":"ongoing","primaryMediaItem":{"state":"failed"}}`, "failed", "failed"},
		{"media paused", `{"state":"paused","primaryMediaItem":{"state":"paused"}}`, "failed", "paused"},
		{"scraped", `{"state":"ongoing","primaryMediaItem":{"state":"scraped"}}`, "downloading", "scraped"},
		{"downloaded", `{"state":"ongoing","primaryMediaItem":{"state":"downloaded"}}`, "downloading", "downloaded"},
		{"partially completed", `{"state":"ongoing","primaryMediaItem":{"state":"partially_completed"}}`, "downloading", "partially_completed"},
		{"indexed, request already completed", `{"state":"completed","primaryMediaItem":{"state":"indexed"}}`, "queued", "indexed"},
		{"unreleased", `{"state":"unreleased","primaryMediaItem":{"state":"unreleased"}}`, "queued", "unreleased"},
		{"unknown media state", `{"state":"ongoing","primaryMediaItem":{"state":"some_new_state"}}`, "queued", "some_new_state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := startFakeRiven(t, map[string]string{
				"itemRequestByExternalIds": `{"data":{"itemRequestByExternalIds":` + tc.request + `}}`,
			})

			resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
				Request:     &pluginv1.RequestDescriptor{MediaType: "movie", ExternalIds: map[string]string{"tmdb": "42"}},
				Targets:     []*pluginv1.TargetRef{{Quality: "1080p", ConnectionId: "c1", ExternalId: "42"}},
				Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
			})
			if err != nil {
				t.Fatalf("CheckStatus: %v", err)
			}
			if len(resp.GetStatuses()) != 1 {
				t.Fatalf("want 1 status, got %d", len(resp.GetStatuses()))
			}
			st := resp.GetStatuses()[0]
			if st.GetStatus() != tc.wantStatus || st.GetExternalStatus() != tc.wantES || st.GetQuality() != "1080p" {
				t.Fatalf("want %s/%q, got %+v", tc.wantStatus, tc.wantES, st)
			}
		})
	}
}

func TestCheckStatusSkipsFailedLookup(t *testing.T) {
	okSrv, _ := startFakeRiven(t, map[string]string{
		"itemRequestByExternalIds": `{"data":{"itemRequestByExternalIds":{"state":"ongoing","primaryMediaItem":{"state":"scraped"}}}}`,
	})
	errSrv, _ := startFakeRiven(t, map[string]string{"itemRequestByExternalIds": unauthenticated})

	resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
		Request: &pluginv1.RequestDescriptor{MediaType: "series", ExternalIds: map[string]string{"tvdb": "99"}},
		Targets: []*pluginv1.TargetRef{
			{Quality: "1080p", ConnectionId: "good", ExternalId: "99"},
			{Quality: "1080p", ConnectionId: "bad", ExternalId: "99"},
		},
		Connections: []*pluginv1.RouterConnection{routerConn("good", okSrv.URL), routerConn("bad", errSrv.URL)},
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if len(resp.GetStatuses()) != 1 || resp.GetStatuses()[0].GetConnectionId() != "good" {
		t.Fatalf("want only the good connection's status, got %+v", resp.GetStatuses())
	}
}

func TestCheckStatusSkipsUnknownConnection(t *testing.T) {
	resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
		Request:     &pluginv1.RequestDescriptor{MediaType: "movie", ExternalIds: map[string]string{"tmdb": "42"}},
		Targets:     []*pluginv1.TargetRef{{Quality: "1080p", ConnectionId: "missing", ExternalId: "42"}},
		Connections: nil,
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if len(resp.GetStatuses()) != 0 {
		t.Fatalf("want 0 statuses, got %d", len(resp.GetStatuses()))
	}
}

func TestTestConnection(t *testing.T) {
	validSrv, _ := startFakeRiven(t, map[string]string{
		"instanceStatus": `{"data":{"instanceStatus":{"restartRequired":false}}}`,
	})
	rejectSrv, _ := startFakeRiven(t, map[string]string{"instanceStatus": unauthenticated})

	cases := []struct {
		name    string
		baseURL string
		wantOK  bool
	}{
		{"valid key", validSrv.URL, true},
		{"rejected key", rejectSrv.URL, false},
		{"unreachable host", "http://127.0.0.1:1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := New().TestConnection(context.Background(), &pluginv1.TestConnectionRequest{
				Connection: routerConn("c1", tc.baseURL),
			})
			if err != nil {
				t.Fatalf("TestConnection: %v", err)
			}
			if resp.GetOk() != tc.wantOK {
				t.Fatalf("want ok=%v, got %+v", tc.wantOK, resp)
			}
		})
	}
}
