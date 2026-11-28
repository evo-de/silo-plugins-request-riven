package router

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/evo-de/silo-plugins-request-riven/internal/riven/riventest"
	"github.com/evo-de/silo-plugins-request-riven/internal/tmdb/tmdbtest"
	"github.com/evo-de/silo-plugins-request-riven/internal/translate"
)

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
			srv := riventest.Start(t, requested)

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
			if len(srv.Calls()) != 1 {
				t.Fatalf("want 1 riven call, got %d", len(srv.Calls()))
			}
			if want := map[string]any{"input": tc.wantInput}; !reflect.DeepEqual(srv.Calls()[0].Variables, want) {
				t.Fatalf("riven received %v, want %v", srv.Calls()[0].Variables, want)
			}
		})
	}
}

// TestFulfillMultipleQualitiesShareOneRequest locks in the fix for a bug where
// only the first requested quality got a target: the host requires one target
// per requested quality, and a tier with none comes back gets marked failed
// even though riven (which has no per-quality profiles) is handling it fine.
func TestFulfillMultipleQualitiesShareOneRequest(t *testing.T) {
	srv := riventest.Start(t, requested)

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
	if len(srv.Calls()) != 1 {
		t.Fatalf("want 1 riven call for 2 qualities on the same connection, got %d", len(srv.Calls()))
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
			srv := riventest.Start(t, requested)

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
			if len(srv.Calls()) != 0 {
				t.Fatalf("want no riven call, got %d", len(srv.Calls()))
			}
		})
	}
}

func TestFulfillPartialContainmentAcrossConnections(t *testing.T) {
	okSrv := riventest.Start(t, requested)
	errSrv := riventest.Start(t, map[string]string{"requestItem": riventest.Unauthenticated})

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
			srv := riventest.Start(t, map[string]string{
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
	okSrv := riventest.Start(t, map[string]string{
		"itemRequestByExternalIds": `{"data":{"itemRequestByExternalIds":{"state":"ongoing","primaryMediaItem":{"state":"scraped"}}}}`,
	})
	errSrv := riventest.Start(t, map[string]string{"itemRequestByExternalIds": riventest.Unauthenticated})

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
	validSrv := riventest.Start(t, map[string]string{
		"instanceStatus": `{"data":{"instanceStatus":{"restartRequired":false}}}`,
	})
	rejectSrv := riventest.Start(t, map[string]string{"instanceStatus": riventest.Unauthenticated})

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

func TestFulfillRequestsOnlyTheSeasonsSiloAsksFor(t *testing.T) {
	const noRequest = `{"data":{"itemRequestByExternalIds":null}}`
	cases := []struct {
		name         string
		seasons      []int32
		existing     string
		wantStatus   string
		wantMessage  string
		wantRequests []any // requestedSeasons riven receives; nil means no requestItem call
	}{
		{
			name:         "new show",
			seasons:      []int32{2, 3},
			existing:     noRequest,
			wantStatus:   "queued",
			wantMessage:  "requested in riven",
			wantRequests: []any{2.0, 3.0},
		},
		{
			name:         "show riven tracks by season",
			seasons:      []int32{2, 3},
			existing:     `{"data":{"itemRequestByExternalIds":{"state":"completed","seasons":[1],"primaryMediaItem":null}}}`,
			wantStatus:   "queued",
			wantMessage:  "requested in riven",
			wantRequests: []any{2.0, 3.0},
		},
		{
			name:        "show riven tracks whole",
			seasons:     []int32{3},
			existing:    `{"data":{"itemRequestByExternalIds":{"state":"ongoing","seasons":null,"primaryMediaItem":null}}}`,
			wantStatus:  "queued",
			wantMessage: "riven already tracks every season",
		},
		{
			name:        "Specials of a show riven tracks whole",
			seasons:     []int32{0, 3},
			existing:    `{"data":{"itemRequestByExternalIds":{"state":"ongoing","seasons":null,"primaryMediaItem":null}}}`,
			wantStatus:  "failed",
			wantMessage: "cannot add Specials",
		},
		{
			name:        "lookup rejected",
			seasons:     []int32{3},
			existing:    riventest.Unauthenticated,
			wantStatus:  "failed",
			wantMessage: "Access denied",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := riventest.Start(t, map[string]string{
				"itemRequestByExternalIds": tc.existing,
				"requestItem(":             `{"data":{"requestItem":true}}`,
			})

			resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
				Request: &pluginv1.RequestDescriptor{
					MediaType:   "series",
					ExternalIds: map[string]string{"tvdb": "371980"},
					Seasons:     tc.seasons,
				},
				Qualities:   []*pluginv1.RequestedQuality{{Id: "1080p"}},
				Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
			})
			if err != nil {
				t.Fatalf("Fulfill: %v", err)
			}
			if len(resp.GetTargets()) != 1 {
				t.Fatalf("want 1 target, got %+v", resp)
			}
			tgt := resp.GetTargets()[0]
			if tgt.GetStatus() != tc.wantStatus || !strings.Contains(tgt.GetMessage(), tc.wantMessage) {
				t.Fatalf("want %s with %q, got %+v", tc.wantStatus, tc.wantMessage, tgt)
			}

			var sent []any
			for _, call := range srv.Calls() {
				if strings.Contains(call.Query, "requestItem(") {
					sent = call.Variables["input"].(map[string]any)["requestedSeasons"].([]any)
				}
			}
			if !reflect.DeepEqual(sent, tc.wantRequests) {
				t.Fatalf("riven was asked for seasons %v, want %v", sent, tc.wantRequests)
			}
		})
	}
}

func TestCheckStatusReadsTheRequestedSeasons(t *testing.T) {
	const showSeasons = `[{"number":1,"state":"completed"},{"number":2,"state":"indexed"},{"number":3,"state":"scraped"},{"number":4,"state":"failed"}]`
	cases := []struct {
		name               string
		seasons            []int32
		request            string
		wantStatus, wantES string
	}{
		{"whole show reads the show", nil, `{"state":"processing","primaryMediaItem":{"state":"completed","seasons":` + showSeasons + `}}`, "completed", "completed"},
		{"finished season", []int32{1}, `{"state":"processing","primaryMediaItem":{"state":"partially_completed","seasons":` + showSeasons + `}}`, "completed", "completed"},
		{"waiting season", []int32{2}, `{"state":"processing","primaryMediaItem":{"state":"partially_completed","seasons":` + showSeasons + `}}`, "queued", "indexed"},
		{"season in progress", []int32{3}, `{"state":"processing","primaryMediaItem":{"state":"partially_completed","seasons":` + showSeasons + `}}`, "downloading", "scraped"},
		{"failed season", []int32{4}, `{"state":"processing","primaryMediaItem":{"state":"partially_completed","seasons":` + showSeasons + `}}`, "failed", "failed"},
		{"finished and waiting", []int32{1, 2}, `{"state":"processing","primaryMediaItem":{"state":"partially_completed","seasons":` + showSeasons + `}}`, "downloading", "partially_completed"},
		{"in progress and waiting", []int32{2, 3}, `{"state":"processing","primaryMediaItem":{"state":"partially_completed","seasons":` + showSeasons + `}}`, "downloading", "scraped"},
		{"failed and waiting", []int32{2, 4}, `{"state":"processing","primaryMediaItem":{"state":"partially_completed","seasons":` + showSeasons + `}}`, "queued", "indexed"},
		{"season riven has not indexed", []int32{5}, `{"state":"ongoing","primaryMediaItem":{"state":"completed","seasons":` + showSeasons + `}}`, "queued", "ongoing"},
		{"show not indexed yet", []int32{1}, `{"state":"requested","primaryMediaItem":null}`, "queued", "requested"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := riventest.Start(t, map[string]string{
				"itemRequestByExternalIds": `{"data":{"itemRequestByExternalIds":` + tc.request + `}}`,
			})

			resp, err := New().CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
				Request:     &pluginv1.RequestDescriptor{MediaType: "series", ExternalIds: map[string]string{"tvdb": "99"}, Seasons: tc.seasons},
				Targets:     []*pluginv1.TargetRef{{Quality: "1080p", ConnectionId: "c1", ExternalId: "99"}},
				Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
			})
			if err != nil {
				t.Fatalf("CheckStatus: %v", err)
			}
			if len(resp.GetStatuses()) != 1 {
				t.Fatalf("want 1 status, got %d", len(resp.GetStatuses()))
			}
			st := resp.GetStatuses()[0]
			if st.GetStatus() != tc.wantStatus || st.GetExternalStatus() != tc.wantES {
				t.Fatalf("want %s/%q, got %+v", tc.wantStatus, tc.wantES, st)
			}
		})
	}
}

// splitShow answers riven-ts's TVDB lookup for a show that riven-ts splits
// into two 12-episode seasons, while Silo counts one 24-episode season.
func splitShow() map[string]string { return riventest.WeeklySeasons("2024-01-07", "2024-07-07") }

var oneLongSeason = []*pluginv1.RequestedSeason{{Number: 1, FirstAirDate: "2024-01-07", EpisodeCount: 24}}

// Season matching itself is tested in translate. This checks that Fulfill
// asks riven-ts for the riven-ts seasons it matched.
func TestFulfillRequestsTheSeasonsTheAirDatesMatch(t *testing.T) {
	stubs := splitShow()
	stubs["itemRequestByExternalIds"] = `{"data":{"itemRequestByExternalIds":null}}`
	stubs["requestItem("] = `{"data":{"requestItem":true}}`
	srv := riventest.Start(t, stubs)

	resp, err := New().Fulfill(context.Background(), &pluginv1.FulfillRequest{
		Request: &pluginv1.RequestDescriptor{
			MediaType: "series", ExternalIds: map[string]string{"tvdb": "99"},
			Seasons: []int32{1}, SeasonDetails: oneLongSeason,
		},
		Connections: []*pluginv1.RouterConnection{routerConn("c1", srv.URL)},
	})
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if tgt := resp.GetTargets()[0]; tgt.GetStatus() != "queued" {
		t.Fatalf("want a queued target, got %+v", tgt)
	}
	calls := srv.Calls()
	last := calls[len(calls)-1]
	if got := last.Variables["input"].(map[string]any)["requestedSeasons"]; !reflect.DeepEqual(got, []any{1.0, 2.0}) {
		t.Fatalf("riven was asked for seasons %v, want [1 2]", got)
	}
}

// renumberedShow answers riven-ts's TVDB lookup for a show that TVDB splits
// into three seasons. TMDB counts TVDB's first two as its season 1, and TVDB's
// third as its season 2, the way it numbers Bleach.
func renumberedShow() map[string]string {
	return riventest.WeeklySeasons("2004-01-04", "2004-04-04", "2022-10-09")
}

const renumberedTMDB = `{"id":30984,"seasons":[
	{"air_date":null,"episode_count":3,"season_number":0},
	{"air_date":"2004-01-04","episode_count":24,"season_number":1},
	{"air_date":"2022-10-09","episode_count":12,"season_number":2}
]}`

func tmdbConn(t *testing.T, id, baseURL, tmdbKey string) *pluginv1.RouterConnection {
	t.Helper()
	c := routerConn(id, baseURL)
	config, err := structpb.NewStruct(map[string]any{"tmdb_api_key": tmdbKey})
	if err != nil {
		t.Fatalf("connection config: %v", err)
	}
	c.Config = config
	return c
}

// Season matching itself is tested in translate. This checks that CheckStatus
// reads the connection's TMDB key and reports the riven-ts seasons it matched.
func TestCheckStatusReadsTheSeasonTMDBDatesMatch(t *testing.T) {
	stubs := renumberedShow()
	stubs["itemRequestByExternalIds"] = `{"data":{"itemRequestByExternalIds":{"state":"processing","seasons":[2,3],"primaryMediaItem":{"state":"partially_completed","seasons":[{"number":1,"state":"completed"},{"number":2,"state":"completed"},{"number":3,"state":"scraped"}]}}}}`
	srv := riventest.Start(t, stubs)
	tmdbSrv := tmdbtest.Start(t, http.StatusOK, renumberedTMDB)

	resp, err := (&Server{translator: translate.New(tmdbSrv.URL)}).CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
		Request: &pluginv1.RequestDescriptor{
			MediaType: "series", ExternalIds: map[string]string{"tvdb": "74796", "tmdb": "30984"},
			Seasons: []int32{2},
		},
		Targets:     []*pluginv1.TargetRef{{Quality: "1080p", ConnectionId: "c1", ExternalId: "74796"}},
		Connections: []*pluginv1.RouterConnection{tmdbConn(t, "c1", srv.URL, "tmdb-key")},
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	// Silo's season 2 is riven-ts's season 3, which is still downloading.
	if st := resp.GetStatuses()[0]; st.GetStatus() != "downloading" || st.GetExternalStatus() != "scraped" {
		t.Fatalf("want downloading/scraped, got %+v", st)
	}
}

// TestCheckStatusAsksEachConnectionOnce checks that a connection's quality
// tiers share one answer. riven-ts has no quality tiers, so asking again for
// each tier would only repeat the same TMDB and riven-ts lookups.
func TestCheckStatusAsksEachConnectionOnce(t *testing.T) {
	stubs := renumberedShow()
	stubs["itemRequestByExternalIds"] = `{"data":{"itemRequestByExternalIds":{"state":"processing","seasons":[3],"primaryMediaItem":{"state":"partially_completed","seasons":[{"number":3,"state":"scraped"}]}}}}`
	srv := riventest.Start(t, stubs)
	tmdbSrv := tmdbtest.Start(t, http.StatusOK, renumberedTMDB)

	resp, err := (&Server{translator: translate.New(tmdbSrv.URL)}).CheckStatus(context.Background(), &pluginv1.CheckStatusRequest{
		Request: &pluginv1.RequestDescriptor{
			MediaType: "series", ExternalIds: map[string]string{"tvdb": "74796", "tmdb": "30984"},
			Seasons: []int32{2},
		},
		Targets: []*pluginv1.TargetRef{
			{Quality: "1080p", ConnectionId: "c1", ExternalId: "74796"},
			{Quality: "2160p", ConnectionId: "c1", ExternalId: "74796"},
		},
		Connections: []*pluginv1.RouterConnection{tmdbConn(t, "c1", srv.URL, "tmdb-key")},
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	statuses := resp.GetStatuses()
	if len(statuses) != 2 || statuses[0].GetQuality() != "1080p" || statuses[1].GetQuality() != "2160p" {
		t.Fatalf("want a status for each quality, in order, got %+v", statuses)
	}
	for _, st := range statuses {
		if st.GetStatus() != "downloading" || st.GetExternalStatus() != "scraped" {
			t.Fatalf("want downloading/scraped, got %+v", st)
		}
	}
	if paths := tmdbSrv.Paths(); len(paths) != 1 {
		t.Fatalf("want one TMDB lookup, got %v", paths)
	}
	if calls := srv.CallsTo("itemRequestByExternalIds"); len(calls) != 1 {
		t.Fatalf("want one status lookup, got %d", len(calls))
	}
	if calls := srv.CallsTo("seasons { number }"); len(calls) != 1 {
		t.Fatalf("want one episode lookup, got %d", len(calls))
	}
}
