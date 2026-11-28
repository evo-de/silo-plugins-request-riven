package riven

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"

	"github.com/evo-de/silo-plugins-request-riven/internal/riven/riventest"
)

func start(t *testing.T, responses map[string]string) (*riventest.Server, *httpclient.Client) {
	t.Helper()
	srv := riventest.Start(t, responses)
	return srv, NewClient(srv.URL, "k")
}

func TestNewClientSendsBearerToken(t *testing.T) {
	srv := riventest.Start(t, map[string]string{"instanceStatus": `{"data":{"instanceStatus":{"restartRequired":false}}}`})

	if err := Ping(context.Background(), NewClient(srv.URL, "riven-key")); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := srv.Calls()[0].Authorization; got != "Bearer riven-key" {
		t.Fatalf("Authorization: want %q, got %q", "Bearer riven-key", got)
	}
}

func TestItemForPicksTheIDRivenTracksBy(t *testing.T) {
	all := map[string]string{"tmdb": "329865", "tvdb": "371980", "imdb": "tt2543164"}
	cases := []struct {
		name        string
		mediaType   MediaType
		ids         map[string]string
		want        Item
		wantMissing string
	}{
		{"movie by TMDB", Movie, all, Item{Type: Movie, ID: "329865"}, ""},
		{"show by TVDB", Show, all, Item{Type: Show, ID: "371980"}, ""},
		{"movie without TMDB", Movie, map[string]string{"tvdb": "1", "imdb": "tt2543164"}, Item{}, "TMDB"},
		{"show without TVDB", Show, map[string]string{"tmdb": "95396", "imdb": "tt11280740"}, Item{}, "TVDB"},
		{"no ids", Movie, nil, Item{}, "TMDB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ItemFor(tc.mediaType, tc.ids)
			if tc.wantMissing != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantMissing) {
					t.Fatalf("want an error naming %s, got %+v, %v", tc.wantMissing, got, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %+v, got %+v, %v", tc.want, got, err)
			}
		})
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
		{
			"show seasons",
			Item{Type: Show, ID: "99", Seasons: []int32{0, 2}},
			map[string]any{"type": "show", "tvdbId": "99", "requestedBy": "silo", "requestedSeasons": []any{0.0, 2.0}},
		},
		{
			"movie ignores seasons",
			Item{Type: Movie, ID: "42", Seasons: []int32{1}},
			map[string]any{"type": "movie", "tmdbId": "42", "requestedBy": "silo"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, client := start(t, map[string]string{"requestItem(": `{"data":{"requestItem":true}}`})

			if err := RequestItem(context.Background(), client, tc.item, "silo"); err != nil {
				t.Fatalf("RequestItem: %v", err)
			}
			calls := srv.Calls()
			if len(calls) != 1 {
				t.Fatalf("want 1 request, got %d", len(calls))
			}
			call := calls[0]
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
	_, client := start(t, map[string]string{"requestItem(": riventest.Unauthenticated})

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
			name:     "show requested by season",
			item:     Item{Type: Show, ID: "99"},
			response: `{"data":{"itemRequestByExternalIds":{"state":"processing","seasons":[1,2],"primaryMediaItem":{"state":"partially_completed","seasons":[{"number":0,"state":"indexed"},{"number":1,"state":"completed"},{"number":2,"state":"scraped"}]}}}}`,
			wantVars: map[string]any{"tvdbId": "99"},
			want: ItemState{
				Request:        "processing",
				Media:          "partially_completed",
				RequestSeasons: []int32{1, 2},
				Seasons:        map[int32]string{0: "indexed", 1: "completed", 2: "scraped"},
			},
		},
		{
			name:     "show requested whole",
			item:     Item{Type: Show, ID: "99"},
			response: `{"data":{"itemRequestByExternalIds":{"state":"ongoing","seasons":null,"primaryMediaItem":{"state":"completed","seasons":[{"number":1,"state":"completed"}]}}}}`,
			wantVars: map[string]any{"tvdbId": "99"},
			want:     ItemState{Request: "ongoing", Media: "completed", Seasons: map[int32]string{1: "completed"}},
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
			srv, client := start(t, map[string]string{"itemRequestByExternalIds(": tc.response})

			got, err := ItemStatus(context.Background(), client, tc.item)
			if err != nil {
				t.Fatalf("ItemStatus: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("state: want %+v, got %+v", tc.want, got)
			}
			call := srv.Calls()[0]
			if !strings.Contains(call.Query, "itemRequestByExternalIds(") {
				t.Fatalf("want the itemRequestByExternalIds query, got: %s", call.Query)
			}
			// riven-ts leaves Specials out of a show's seasons unless asked.
			if !strings.Contains(call.Query, "seasons(includeSpecials: true)") {
				t.Fatalf("want the show's seasons with Specials, got: %s", call.Query)
			}
			if !reflect.DeepEqual(call.Variables, tc.wantVars) {
				t.Fatalf("variables: want %v, got %v", tc.wantVars, call.Variables)
			}
		})
	}
}

func TestItemStatusSurfacesGraphQLError(t *testing.T) {
	_, client := start(t, map[string]string{"itemRequestByExternalIds(": riventest.Unauthenticated})

	if _, err := ItemStatus(context.Background(), client, Item{Type: Show, ID: "99"}); err == nil {
		t.Fatal("want an error for an errors response")
	}
}

func TestPing(t *testing.T) {
	_, client := start(t, map[string]string{"instanceStatus": `{"data":{"instanceStatus":{"restartRequired":false}}}`})
	if err := Ping(context.Background(), client); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	_, client = start(t, map[string]string{"instanceStatus": riventest.Unauthenticated})
	if err := Ping(context.Background(), client); err == nil {
		t.Fatal("want an error when riven rejects the key")
	}
}

func TestEpisodes(t *testing.T) {
	srv, client := start(t, map[string]string{
		"seasons { number }": `{"data":{"tmdbItem":{"seasons":[{"number":0},{"number":1},{"number":2}]}}}`,
		"season(number:":     `{"data":{"tmdbItem":{"s1":{"episodes":[{"number":1,"airDate":"2025-03-30T00:00:00.000Z"},{"number":2,"airDate":null}]},"s2":{"episodes":[{"number":1,"airDate":"2026-01-04T00:00:00.000Z"}]}}}}`,
	})

	got, err := Episodes(context.Background(), client, "99")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	want := []Episode{{Season: 1, Number: 1, AirDate: "2025-03-30"}, {Season: 1, Number: 2}, {Season: 2, Number: 1, AirDate: "2026-01-04"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("episodes: want %+v, got %+v", want, got)
	}
	calls := srv.Calls()
	if len(calls) != 2 {
		t.Fatalf("want 2 requests, got %d", len(calls))
	}
	for _, call := range calls {
		if !reflect.DeepEqual(call.Variables, map[string]any{"id": "99"}) {
			t.Fatalf("variables: want the TVDB id, got %v", call.Variables)
		}
	}
	// Specials are not regular episodes, so their season is not fetched.
	episodesQuery := calls[1].Query
	if !strings.Contains(episodesQuery, "s1: season(number: 1)") || !strings.Contains(episodesQuery, "s2: season(number: 2)") ||
		strings.Contains(episodesQuery, "season(number: 0)") {
		t.Fatalf("want seasons 1 and 2 without Specials, got: %s", episodesQuery)
	}
}

func TestEpisodesOfUnknownShow(t *testing.T) {
	srv, client := start(t, map[string]string{"seasons { number }": `{"data":{"tmdbItem":null}}`})

	got, err := Episodes(context.Background(), client, "99")
	if err != nil || got != nil {
		t.Fatalf("want no episodes and no error, got %v, %v", got, err)
	}
	if calls := srv.Calls(); len(calls) != 1 {
		t.Fatalf("want 1 request, got %d", len(calls))
	}
}
