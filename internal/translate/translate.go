// Package translate turns a Silo request into the item the plugin asks
// riven-ts for. A movie needs only its TMDB id. A series that names seasons
// also needs season matching: Silo numbers seasons the way TMDB does and
// riven-ts the way TVDB does, and the same episodes can sit in seasons with
// different numbers.
package translate

import (
	"context"
	"slices"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"

	"github.com/evo-de/silo-plugins-request-riven/internal/riven"
	"github.com/evo-de/silo-plugins-request-riven/internal/tmdb"
)

// Translator translates Silo requests. When Silo sends no air dates for a
// season request, it reads them from the TMDB API at its base URL.
type Translator struct {
	tmdbURL string
}

// New returns a Translator that reads TMDB at tmdbURL, which is tmdb.BaseURL
// outside tests.
func New(tmdbURL string) *Translator { return &Translator{tmdbURL: tmdbURL} }

// Request is a Silo request translated as far as it can be without asking
// riven-ts or TMDB. ItemOn finishes the translation for one connection.
type Request struct {
	tmdbURL string
	// item has no seasons yet: its seasons are riven-ts seasons, which only
	// season matching on a connection can tell.
	item riven.Item
	// seasons are the Silo seasons asked for. Empty means the whole series.
	seasons []int32
	// details are the Silo seasons' air dates and episode counts, when Silo
	// sends them (Silo-Server/silo-server#4484).
	details []siloSeason
	tmdbID  string
}

// Request translates a Silo request without asking anyone. It fails when
// riven-ts cannot track the media, and the failure applies to the whole
// request rather than to one connection.
func (t *Translator) Request(d *pluginv1.RequestDescriptor) (Request, error) {
	ids := d.GetExternalIds()
	if d.GetMediaType() != "series" {
		item, err := riven.ItemFor(riven.Movie, ids)
		if err != nil {
			return Request{}, err
		}
		return Request{tmdbURL: t.tmdbURL, item: item}, nil
	}
	item, err := riven.ItemFor(riven.Show, ids)
	if err != nil {
		return Request{}, err
	}
	r := Request{tmdbURL: t.tmdbURL, item: item, seasons: d.GetSeasons(), tmdbID: ids["tmdb"]}
	for _, s := range d.GetSeasonDetails() {
		r.details = append(r.details, siloSeason{Number: s.GetNumber(), FirstAirDate: s.GetFirstAirDate(), EpisodeCount: s.GetEpisodeCount()})
	}
	return r, nil
}

// LookupID returns the id riven-ts tracks the request's media by.
func (r Request) LookupID() string { return r.item.ID }

// ItemOn returns the item to ask one riven-ts connection for. A movie or a
// whole series needs no lookups. For a series that names seasons, it matches
// each Silo season to the riven-ts seasons that hold its episodes, reading the
// show's episodes through client. When Silo sent no air dates and tmdbKey is
// set, it first reads them from TMDB. A Silo season it cannot match keeps its
// number, as the best guess at its riven-ts season.
func (r Request) ItemOn(ctx context.Context, client *httpclient.Client, tmdbKey string) (riven.Item, error) {
	item := r.item
	if len(r.seasons) == 0 {
		return item, nil
	}
	details, err := r.seasonDetails(ctx, tmdbKey)
	if err != nil {
		return riven.Item{}, err
	}
	item.Seasons = slices.Clone(r.seasons)
	if !slices.ContainsFunc(r.seasons, func(number int32) bool { return usableDetail(details, number) != nil }) {
		return item, nil
	}
	episodes, err := riven.Episodes(ctx, client, item.ID)
	if err != nil {
		return riven.Item{}, err
	}
	item.Seasons = matchSeasons(r.seasons, details, episodes)
	return item, nil
}

// seasonDetails returns the air dates and episode counts of the requested
// Silo seasons: the ones Silo sent, or else TMDB's when there is a TMDB key and
// a TMDB id. Without either it returns none, and the seasons keep their
// numbers.
func (r Request) seasonDetails(ctx context.Context, tmdbKey string) ([]siloSeason, error) {
	if len(r.details) > 0 || tmdbKey == "" || r.tmdbID == "" {
		return r.details, nil
	}
	seasons, err := tmdb.Seasons(ctx, r.tmdbURL, tmdbKey, r.tmdbID)
	if err != nil {
		return nil, err
	}
	var details []siloSeason
	for _, s := range seasons {
		if slices.Contains(r.seasons, s.Number) {
			details = append(details, siloSeason{Number: s.Number, FirstAirDate: s.AirDate, EpisodeCount: s.EpisodeCount})
		}
	}
	return details, nil
}
