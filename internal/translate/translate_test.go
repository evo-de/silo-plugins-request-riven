package translate_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/evo-de/silo-plugins-request-riven/internal/riven"
	"github.com/evo-de/silo-plugins-request-riven/internal/riven/riventest"
	"github.com/evo-de/silo-plugins-request-riven/internal/tmdb/tmdbtest"
	"github.com/evo-de/silo-plugins-request-riven/internal/translate"
)

// splitShow answers riven-ts's episode lookup for a show that riven-ts splits
// into two 12-episode seasons, while Silo counts one 24-episode season.
func splitShow() map[string]string { return riventest.WeeklySeasons("2024-01-07", "2024-07-07") }

var oneLongSeason = []*pluginv1.RequestedSeason{{Number: 1, FirstAirDate: "2024-01-07", EpisodeCount: 24}}

// renumberedShow answers riven-ts's episode lookup for a show that TVDB splits
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

func itemOn(t *testing.T, tr *translate.Translator, d *pluginv1.RequestDescriptor, rivenURL, tmdbKey string) (riven.Item, error) {
	t.Helper()
	r, err := tr.Request(d)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	return r.ItemOn(context.Background(), riven.NewClient(rivenURL, "k"), tmdbKey)
}

func TestRequestTranslatesTheMediaType(t *testing.T) {
	ids := map[string]string{"tmdb": "329865", "tvdb": "371980", "imdb": "tt2543164"}
	cases := []struct {
		name, mediaType string
		want            riven.Item
	}{
		{"movie", "movie", riven.Item{Type: riven.Movie, ID: "329865"}},
		{"whole series", "series", riven.Item{Type: riven.Show, ID: "371980"}},
		{"anything else is a movie", "", riven.Item{Type: riven.Movie, ID: "329865"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rivenSrv := riventest.Start(t, nil)
			tmdbSrv := tmdbtest.Start(t, http.StatusOK, renumberedTMDB)

			d := &pluginv1.RequestDescriptor{MediaType: tc.mediaType, ExternalIds: ids}
			r, err := translate.New(tmdbSrv.URL).Request(d)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			if r.LookupID() != tc.want.ID {
				t.Fatalf("lookup id: want %q, got %q", tc.want.ID, r.LookupID())
			}
			got, err := r.ItemOn(context.Background(), riven.NewClient(rivenSrv.URL, "k"), "tmdb-key")
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %+v, got %+v, %v", tc.want, got, err)
			}
			// A movie or a whole series needs no lookups.
			if len(rivenSrv.Calls()) != 0 || len(tmdbSrv.Requests()) != 0 {
				t.Fatalf("want no lookups, got riven %d, TMDB %d", len(rivenSrv.Calls()), len(tmdbSrv.Requests()))
			}
		})
	}
}

func TestRequestRefusesMediaRivenCannotTrack(t *testing.T) {
	_, err := translate.New("").Request(&pluginv1.RequestDescriptor{
		MediaType: "series", ExternalIds: map[string]string{"tmdb": "95396", "imdb": "tt11280740"}, Seasons: []int32{1},
	})
	if err == nil || !strings.Contains(err.Error(), "TVDB") {
		t.Fatalf("want an error naming TVDB, got %v", err)
	}
}

func TestItemOnMatchesSeasonsByAirDate(t *testing.T) {
	srv := riventest.Start(t, splitShow())

	got, err := itemOn(t, translate.New(""), &pluginv1.RequestDescriptor{
		MediaType: "series", ExternalIds: map[string]string{"tvdb": "99"},
		Seasons: []int32{1}, SeasonDetails: oneLongSeason,
	}, srv.URL, "")
	if err != nil {
		t.Fatalf("ItemOn: %v", err)
	}
	// Silo's season 1 is riven-ts's seasons 1 and 2.
	if want := (riven.Item{Type: riven.Show, ID: "99", Seasons: []int32{1, 2}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
	for _, call := range srv.Calls() {
		if !reflect.DeepEqual(call.Variables, map[string]any{"id": "99"}) {
			t.Fatalf("want the episodes looked up by TVDB id, got %v", call.Variables)
		}
	}
}

func TestItemOnReadsMissingAirDatesFromTMDB(t *testing.T) {
	both := map[string]string{"tvdb": "74796", "tmdb": "30984"}
	cases := []struct {
		name          string
		tmdbKey       string
		ids           map[string]string
		details       []*pluginv1.RequestedSeason
		wantSeasons   []int32
		wantTMDBPaths []string
	}{
		{
			name:          "TMDB numbers the season differently",
			tmdbKey:       "tmdb-key",
			ids:           both,
			wantSeasons:   []int32{3},
			wantTMDBPaths: []string{"/tv/30984"},
		},
		{
			name:        "Silo sent air dates",
			tmdbKey:     "tmdb-key",
			ids:         both,
			details:     []*pluginv1.RequestedSeason{{Number: 2, FirstAirDate: "2004-01-04", EpisodeCount: 24}},
			wantSeasons: []int32{1, 2},
		},
		{
			name:        "no TMDB key",
			ids:         both,
			wantSeasons: []int32{2},
		},
		{
			name:        "no TMDB id",
			tmdbKey:     "tmdb-key",
			ids:         map[string]string{"tvdb": "74796"},
			wantSeasons: []int32{2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rivenSrv := riventest.Start(t, renumberedShow())
			tmdbSrv := tmdbtest.Start(t, http.StatusOK, renumberedTMDB)

			got, err := itemOn(t, translate.New(tmdbSrv.URL), &pluginv1.RequestDescriptor{
				MediaType: "series", ExternalIds: tc.ids,
				Seasons: []int32{2}, SeasonDetails: tc.details,
			}, rivenSrv.URL, tc.tmdbKey)
			if err != nil {
				t.Fatalf("ItemOn: %v", err)
			}
			if !reflect.DeepEqual(got.Seasons, tc.wantSeasons) {
				t.Fatalf("want riven-ts seasons %v, got %v", tc.wantSeasons, got.Seasons)
			}
			if !reflect.DeepEqual(tmdbSrv.Paths(), tc.wantTMDBPaths) {
				t.Fatalf("TMDB received %v, want %v", tmdbSrv.Paths(), tc.wantTMDBPaths)
			}
		})
	}
}

func TestItemOnFailsWhenALookupFails(t *testing.T) {
	cases := []struct {
		name        string
		riven       map[string]string
		tmdbStatus  int
		details     []*pluginv1.RequestedSeason
		wantMessage string
	}{
		{
			name:        "riven-ts episodes",
			riven:       map[string]string{"seasons { number }": riventest.Unauthenticated},
			tmdbStatus:  http.StatusOK,
			details:     oneLongSeason,
			wantMessage: "Access denied",
		},
		{
			name:        "TMDB seasons",
			riven:       renumberedShow(),
			tmdbStatus:  http.StatusUnauthorized,
			wantMessage: "401",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rivenSrv := riventest.Start(t, tc.riven)
			tmdbSrv := tmdbtest.Start(t, tc.tmdbStatus, renumberedTMDB)

			_, err := itemOn(t, translate.New(tmdbSrv.URL), &pluginv1.RequestDescriptor{
				MediaType: "series", ExternalIds: map[string]string{"tvdb": "74796", "tmdb": "30984"},
				Seasons: []int32{1}, SeasonDetails: tc.details,
			}, rivenSrv.URL, "tmdb-key")
			if err == nil || !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("want an error containing %q, got %v", tc.wantMessage, err)
			}
		})
	}
}
