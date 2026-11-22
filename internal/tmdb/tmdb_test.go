package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const v4Token = "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJrIn0.c2lnbmF0dXJl"

const bleach = `{"id":30984,"name":"Bleach","seasons":[
	{"air_date":null,"episode_count":3,"id":1,"name":"Specials","season_number":0},
	{"air_date":"2004-10-05","episode_count":366,"id":2,"name":"Season 1","season_number":1},
	{"air_date":"2022-10-11","episode_count":50,"id":3,"name":"Thousand-Year Blood War","season_number":2}
]}`

func startTMDB(t *testing.T, status int, body string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var received []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &received
}

func TestSeasonsReadsTheShowsSeasons(t *testing.T) {
	srv, received := startTMDB(t, http.StatusOK, bleach)

	seasons, err := Seasons(context.Background(), srv.URL, v4Token, "30984")
	if err != nil {
		t.Fatalf("Seasons: %v", err)
	}
	want := []Season{
		{Number: 0, AirDate: "", EpisodeCount: 3},
		{Number: 1, AirDate: "2004-10-05", EpisodeCount: 366},
		{Number: 2, AirDate: "2022-10-11", EpisodeCount: 50},
	}
	if !reflect.DeepEqual(seasons, want) {
		t.Fatalf("want %+v, got %+v", want, seasons)
	}
	if len(*received) != 1 || (*received)[0].Method != http.MethodGet || (*received)[0].URL.Path != "/tv/30984" {
		t.Fatalf("want one GET /tv/30984, got %d requests", len(*received))
	}
}

func TestSeasonsSendsTheKeyTheWayTMDBReadsIt(t *testing.T) {
	const v3Key = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name       string
		key        string
		wantHeader string
		wantQuery  string
	}{
		{"v4 API Read Access Token", v4Token, "Bearer " + v4Token, ""},
		{"v3 API key", v3Key, "", v3Key},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, received := startTMDB(t, http.StatusOK, bleach)

			if _, err := Seasons(context.Background(), srv.URL, tc.key, "30984"); err != nil {
				t.Fatalf("Seasons: %v", err)
			}
			r := (*received)[0]
			if got := r.Header.Get("Authorization"); got != tc.wantHeader {
				t.Fatalf("Authorization: want %q, got %q", tc.wantHeader, got)
			}
			if got := r.URL.Query().Get("api_key"); got != tc.wantQuery {
				t.Fatalf("api_key: want %q, got %q", tc.wantQuery, got)
			}
		})
	}
}

func TestSeasonsFailsOnAnErrorStatus(t *testing.T) {
	srv, _ := startTMDB(t, http.StatusUnauthorized, `{"status_code":7,"status_message":"Invalid API key: You must be granted a valid key.","success":false}`)

	seasons, err := Seasons(context.Background(), srv.URL, v4Token, "30984")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want an error naming status 401, got seasons %+v, err %v", seasons, err)
	}
}

func TestSeasonsKeepsTheKeyOutOfTransportErrors(t *testing.T) {
	const v3Key = "0123456789abcdef0123456789abcdef"

	_, err := Seasons(context.Background(), "http://127.0.0.1:1", v3Key, "30984")
	if err == nil || strings.Contains(err.Error(), v3Key) {
		t.Fatalf("want an error without the key, got %v", err)
	}
}
