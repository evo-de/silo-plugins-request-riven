package router

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"

	"github.com/evo-de/silo-plugins-request-riven/internal/riven"
	"github.com/evo-de/silo-plugins-request-riven/internal/tmdb"
)

// Server implements the request_router.v1 RPCs. It is stateless.
type Server struct {
	pluginv1.UnimplementedRequestRouterServer
	tmdbURL string
}

// New returns a ready-to-serve request router.
func New() *Server { return &Server{tmdbURL: tmdb.BaseURL} }

const requestedBySilo = "silo"

// rivenItem never falls back to IMDb. riven-ts accepts an IMDb-only request
// and then never indexes it. A movie fails with "Item request is missing
// tmdbId", and the TVDB indexer cannot look up a show by IMDb id.
func rivenItem(d *pluginv1.RequestDescriptor) (riven.Item, error) {
	ids := d.GetExternalIds()
	if d.GetMediaType() == "series" {
		if id := ids["tvdb"]; id != "" {
			return riven.Item{Type: riven.Show, ID: id, Seasons: d.GetSeasons()}, nil
		}
		return riven.Item{}, errors.New("request has no TVDB id; riven-ts tracks shows by TVDB id")
	}
	if id := ids["tmdb"]; id != "" {
		return riven.Item{Type: riven.Movie, ID: id}, nil
	}
	return riven.Item{}, errors.New("request has no TMDB id; riven-ts tracks movies by TMDB id")
}

func (s *Server) Fulfill(ctx context.Context, req *pluginv1.FulfillRequest) (*pluginv1.FulfillResponse, error) {
	item, err := rivenItem(req.GetRequest())
	if err != nil {
		// One request-level failure, not N identical per-target failures.
		return &pluginv1.FulfillResponse{Message: err.Error()}, nil
	}

	qualities := req.GetQualities()
	if len(qualities) == 0 {
		qualities = []*pluginv1.RequestedQuality{{Id: "any"}}
	}

	var targets []*pluginv1.FulfillmentTarget
	for _, c := range req.GetConnections() {
		conn := connectionFromRouter(c)
		status := "queued"
		message, err := s.submit(ctx, conn, item, req.GetRequest())
		if err != nil {
			status, message = "failed", err.Error()
		}
		// Silo marks a requested quality with no target as failed, so every
		// quality gets a copy of the one request's outcome.
		for _, q := range qualities {
			targets = append(targets, &pluginv1.FulfillmentTarget{
				Quality:      q.GetId(),
				ConnectionId: conn.ID,
				ExternalId:   item.ID,
				Status:       status,
				Message:      message,
			})
		}
	}
	if len(targets) == 0 {
		return &pluginv1.FulfillResponse{Message: "no riven connection configured"}, nil
	}
	return &pluginv1.FulfillResponse{Targets: targets}, nil
}

// seasonDetails returns the first air date and episode count of each requested
// season. Silo does not send them yet (Silo-Server/silo-server#4484), so when
// the connection has a TMDB key, the plugin reads them from TMDB. Without a key
// or a TMDB id it returns none, and the seasons keep their numbers.
func (s *Server) seasonDetails(ctx context.Context, conn Connection, d *pluginv1.RequestDescriptor) ([]*pluginv1.RequestedSeason, error) {
	if details := d.GetSeasonDetails(); len(details) > 0 {
		return details, nil
	}
	showID := d.GetExternalIds()["tmdb"]
	if d.GetMediaType() != "series" || len(d.GetSeasons()) == 0 || conn.TMDBKey == "" || showID == "" {
		return nil, nil
	}
	seasons, err := tmdb.Seasons(ctx, s.tmdbURL, conn.TMDBKey, showID)
	if err != nil {
		return nil, err
	}
	var details []*pluginv1.RequestedSeason
	for _, season := range seasons {
		if slices.Contains(d.GetSeasons(), season.Number) {
			details = append(details, &pluginv1.RequestedSeason{Number: season.Number, FirstAirDate: season.AirDate, EpisodeCount: season.EpisodeCount})
		}
	}
	return details, nil
}

// resolveSeasons turns the item's seasons from Silo's numbers, which follow
// TMDB, into riven-ts's, which follow TVDB. It looks up the show's episodes
// only when it has a season's first air date and episode count.
func resolveSeasons(ctx context.Context, client *httpclient.Client, item riven.Item, details []*pluginv1.RequestedSeason) (riven.Item, error) {
	if !slices.ContainsFunc(item.Seasons, func(number int32) bool { return usableDetail(details, number) != nil }) {
		return item, nil
	}
	episodes, err := riven.Episodes(ctx, client, item.ID)
	if err != nil {
		return item, err
	}
	item.Seasons = rivenSeasons(item.Seasons, details, episodes)
	return item, nil
}

// usableDetail returns the detail for a regular season, or nil when there is
// none or it has no date or no episode count.
func usableDetail(details []*pluginv1.RequestedSeason, number int32) *pluginv1.RequestedSeason {
	if number == 0 {
		return nil
	}
	for _, d := range details {
		if d.GetNumber() == number && d.GetFirstAirDate() != "" && d.GetEpisodeCount() > 0 {
			return d
		}
	}
	return nil
}

// rivenSeasons finds each requested season the way Silo's request contract
// describes. The season is the first episode_count regular episodes, in airing
// order, that air on or after its first air date, or the ones that do when
// fewer match. riven-ts fetches whole seasons, so the result is every riven-ts
// season those episodes belong to. Specials, a season without a usable detail,
// and a season no episode matches keep their number.
func rivenSeasons(seasons []int32, details []*pluginv1.RequestedSeason, episodes []riven.Episode) []int32 {
	aired := slices.DeleteFunc(slices.Clone(episodes), func(e riven.Episode) bool { return e.AirDate == "" })
	slices.SortFunc(aired, func(a, b riven.Episode) int {
		return cmp.Or(strings.Compare(a.AirDate, b.AirDate), cmp.Compare(a.Season, b.Season), cmp.Compare(a.Number, b.Number))
	})

	var out []int32
	for _, number := range seasons {
		var matched []riven.Episode
		if d := usableDetail(details, number); d != nil {
			start := slices.IndexFunc(aired, func(e riven.Episode) bool { return e.AirDate >= d.GetFirstAirDate() })
			if start >= 0 {
				matched = aired[start:min(start+int(d.GetEpisodeCount()), len(aired))]
			}
		}
		if len(matched) == 0 {
			out = append(out, number)
		}
		for _, e := range matched {
			out = append(out, e.Season)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// submit asks riven-ts for the item, with its seasons in riven-ts's numbers.
// It does not send a request for some seasons of a show that riven-ts tracks as
// a whole. riven-ts would replace the whole-show request with those seasons and
// stop tracking the others when it next indexes the show.
func (s *Server) submit(ctx context.Context, conn Connection, item riven.Item, d *pluginv1.RequestDescriptor) (string, error) {
	details, err := s.seasonDetails(ctx, conn, d)
	if err != nil {
		return "", err
	}
	client := riven.NewClient(conn.BaseURL, conn.APIKey)
	item, err = resolveSeasons(ctx, client, item, details)
	if err != nil {
		return "", err
	}
	if item.Type == riven.Show && len(item.Seasons) > 0 {
		state, err := riven.ItemStatus(ctx, client, item)
		if err != nil {
			return "", err
		}
		if state.Request != "" && state.RequestSeasons == nil {
			if slices.Contains(item.Seasons, 0) {
				return "", errors.New("riven-ts tracks every season of this series except Specials, and cannot add Specials without dropping the other seasons")
			}
			return "riven already tracks every season", nil
		}
	}
	return "requested in riven", riven.RequestItem(ctx, client, item, requestedBySilo)
}

func (s *Server) CheckStatus(ctx context.Context, req *pluginv1.CheckStatusRequest) (*pluginv1.CheckStatusResponse, error) {
	item, err := rivenItem(req.GetRequest())
	if err != nil {
		return &pluginv1.CheckStatusResponse{}, nil
	}

	byID := make(map[string]Connection, len(req.GetConnections()))
	for _, c := range req.GetConnections() {
		conn := connectionFromRouter(c)
		byID[conn.ID] = conn
	}

	var statuses []*pluginv1.TargetStatus
	for _, tref := range req.GetTargets() {
		conn, ok := byID[tref.GetConnectionId()]
		if !ok {
			continue
		}
		details, err := s.seasonDetails(ctx, conn, req.GetRequest())
		if err != nil {
			continue
		}
		client := riven.NewClient(conn.BaseURL, conn.APIKey)
		resolved, err := resolveSeasons(ctx, client, item, details)
		if err != nil {
			continue
		}
		state, err := riven.ItemStatus(ctx, client, resolved)
		if err != nil {
			continue
		}
		if len(resolved.Seasons) > 0 && state.Seasons != nil {
			state.Media = seasonsState(state.Seasons, resolved.Seasons)
		}
		status, externalStatus := mapState(state)
		statuses = append(statuses, &pluginv1.TargetStatus{
			Quality:        tref.GetQuality(),
			ConnectionId:   tref.GetConnectionId(),
			Status:         status,
			ExternalStatus: externalStatus,
		})
	}
	return &pluginv1.CheckStatusResponse{Statuses: statuses}, nil
}

var mediaStatuses = map[string]string{
	"completed":           "completed",
	"failed":              "failed",
	"paused":              "failed",
	"scraped":             "downloading",
	"downloaded":          "downloading",
	"partially_completed": "downloading",
}

// seasonsState summarises the requested seasons the way riven-ts summarises a
// show from its requested seasons. Seasons that all share a state give that
// state. Otherwise any finished season gives partially_completed, any season in
// progress gives scraped, and the rest give indexed. A season riven-ts does not
// track yet has no state.
func seasonsState(seasons map[int32]string, requested []int32) string {
	counts := make(map[string]int)
	for _, number := range requested {
		counts[seasons[number]]++
	}
	if len(counts) == 1 {
		for state := range counts {
			return state
		}
	}
	switch {
	case counts["completed"] > 0 || counts["partially_completed"] > 0:
		return "partially_completed"
	case counts["scraped"] > 0 || counts["downloaded"] > 0:
		return "scraped"
	}
	return "indexed"
}

// mapState reads progress from the primary media item, not the request,
// because riven-ts marks a released movie's request completed as soon as it
// is indexed.
func mapState(s riven.ItemState) (status, externalStatus string) {
	externalStatus = s.Request
	if s.Media != "" {
		externalStatus = s.Media
	}
	if s.Request == "failed" {
		return "failed", externalStatus
	}
	if status, ok := mediaStatuses[s.Media]; ok {
		return status, externalStatus
	}
	return "queued", externalStatus
}

func (s *Server) TestConnection(ctx context.Context, req *pluginv1.TestConnectionRequest) (*pluginv1.TestConnectionResponse, error) {
	conn := connectionFromRouter(req.GetConnection())
	if err := riven.Ping(ctx, riven.NewClient(conn.BaseURL, conn.APIKey)); err != nil {
		return &pluginv1.TestConnectionResponse{Ok: false, Message: err.Error()}, nil
	}
	return &pluginv1.TestConnectionResponse{Ok: true, Message: "connection successful"}, nil
}

// ListConfigOptions returns no dynamic options: riven's connection config has
// no dynamic-options fields. Returned empty (not Unimplemented) so the host's
// options probe gets a clean answer.
func (s *Server) ListConfigOptions(ctx context.Context, req *pluginv1.ListConfigOptionsRequest) (*pluginv1.ListConfigOptionsResponse, error) {
	return &pluginv1.ListConfigOptionsResponse{}, nil
}

// Validate has no cross-field rules to check: the connection config has one
// optional field. Returned empty so the host's save-time Validate succeeds.
func (s *Server) Validate(ctx context.Context, req *pluginv1.ValidateRequest) (*pluginv1.ValidateResponse, error) {
	return &pluginv1.ValidateResponse{}, nil
}
