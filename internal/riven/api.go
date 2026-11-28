package riven

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"
)

const graphQLPath = "/graphql"

func NewClient(baseURL, apiKey string) *httpclient.Client {
	return httpclient.New(baseURL, apiKey, &http.Client{
		// Keep the SDK's default timeout, which a custom http.Client replaces.
		Timeout: 2 * time.Minute,
		// The SDK only sends the key as X-Api-Key, which riven-ts ignores, so
		// the transport adds the Authorization header riven-ts reads.
		Transport: bearerTransport(apiKey),
	})
}

type bearerTransport string

func (key bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(key))
	return http.DefaultTransport.RoundTrip(r)
}

type MediaType string

const (
	Movie MediaType = "movie"
	Show  MediaType = "show"
)

type Item struct {
	Type MediaType
	ID   string
	// Seasons lists the requested season numbers of a show. Empty means the
	// whole show.
	Seasons []int32
}

// ItemFor returns the item riven-ts tracks the media by: a movie by its TMDB
// id, a show by its TVDB id. ids holds external ids keyed "tmdb", "tvdb" and
// "imdb". The item has no seasons, so it stands for a whole show.
//
// It never falls back to IMDb. riven-ts accepts an IMDb-only request and then
// never indexes it. A movie fails with "Item request is missing tmdbId", and
// the TVDB indexer cannot look up a show by IMDb id.
func ItemFor(mediaType MediaType, ids map[string]string) (Item, error) {
	if mediaType == Show {
		if id := ids["tvdb"]; id != "" {
			return Item{Type: Show, ID: id}, nil
		}
		return Item{}, errors.New("request has no TVDB id; riven-ts tracks shows by TVDB id")
	}
	if id := ids["tmdb"]; id != "" {
		return Item{Type: Movie, ID: id}, nil
	}
	return Item{}, errors.New("request has no TMDB id; riven-ts tracks movies by TMDB id")
}

func (i Item) idField() string {
	if i.Type == Show {
		return "tvdbId"
	}
	return "tmdbId"
}

type ItemState struct {
	Request string
	Media   string
	// RequestSeasons is the request's season list. Nil means riven-ts
	// tracks every season but Specials.
	RequestSeasons []int32
	// Seasons maps each season riven-ts tracks for the show to its state,
	// once riven-ts has indexed the show.
	Seasons map[int32]string
}

type gqlRequest struct {
	Query     string `json:"query"`
	Variables any    `json:"variables,omitempty"`
}

type gqlError struct {
	Message string `json:"message"`
}

type gqlEnvelope[T any] struct {
	Data   T          `json:"data"`
	Errors []gqlError `json:"errors,omitempty"`
}

func do[T any](ctx context.Context, client *httpclient.Client, query string, variables any) (T, error) {
	var envelope gqlEnvelope[T]
	if err := client.PostJSON(ctx, graphQLPath, gqlRequest{Query: query, Variables: variables}, &envelope); err != nil {
		var zero T
		return zero, err
	}
	if len(envelope.Errors) > 0 {
		var zero T
		return zero, fmt.Errorf("riven: %s", envelope.Errors[0].Message)
	}
	return envelope.Data, nil
}

// Ping reads an authorized field because riven-ts answers public fields such
// as __typename without a key.
func Ping(ctx context.Context, client *httpclient.Client) error {
	_, err := do[struct{}](ctx, client, `query { instanceStatus { restartRequired } }`, nil)
	return err
}

const requestItemMutation = `
mutation($input: RequestItemInput!) {
  requestItem(input: $input)
}`

// RequestItem returns once riven-ts accepts the request, before riven-ts
// processes it, so a duplicate or a later failure never reaches the caller.
func RequestItem(ctx context.Context, client *httpclient.Client, item Item, requestedBy string) error {
	input := map[string]any{
		"type":         item.Type,
		"requestedBy":  requestedBy,
		item.idField(): item.ID,
	}
	if item.Type == Show && len(item.Seasons) > 0 {
		input["requestedSeasons"] = item.Seasons
	}
	_, err := do[struct{}](ctx, client, requestItemMutation, map[string]any{"input": input})
	return err
}

// itemStatusQuery leaves out externalRequestId: riven-ts stores it as null
// despite its String! type, and selecting it nulls the whole result. A show's
// seasons field lists only the seasons riven-ts tracks, and lists Specials
// only when asked.
const itemStatusQuery = `
query($tmdbId: String, $tvdbId: String) {
  itemRequestByExternalIds(tmdbId: $tmdbId, tvdbId: $tvdbId) {
    state
    seasons
    primaryMediaItem {
      ... on Movie { state }
      ... on Show { state seasons(includeSpecials: true) { number state } }
    }
  }
}`

// ItemStatus passes only the item's own id, because riven-ts matches a request
// against every id it is given.
func ItemStatus(ctx context.Context, client *httpclient.Client, item Item) (ItemState, error) {
	type data struct {
		Request struct {
			State            string  `json:"state"`
			Seasons          []int32 `json:"seasons"`
			PrimaryMediaItem struct {
				State   string `json:"state"`
				Seasons []struct {
					Number int32  `json:"number"`
					State  string `json:"state"`
				} `json:"seasons"`
			} `json:"primaryMediaItem"`
		} `json:"itemRequestByExternalIds"`
	}
	d, err := do[data](ctx, client, itemStatusQuery, map[string]any{item.idField(): item.ID})
	if err != nil {
		return ItemState{}, err
	}
	state := ItemState{
		Request:        d.Request.State,
		Media:          d.Request.PrimaryMediaItem.State,
		RequestSeasons: d.Request.Seasons,
	}
	for _, season := range d.Request.PrimaryMediaItem.Seasons {
		if state.Seasons == nil {
			state.Seasons = make(map[int32]string)
		}
		state.Seasons[season.Number] = season.State
	}
	return state, nil
}

// Episode is one regular episode of a show, numbered the way riven-ts indexes
// shows: by TVDB, in official order.
type Episode struct {
	Season int32
	Number int32
	// AirDate is YYYY-MM-DD, or empty when TVDB has no date for the episode.
	AirDate string
}

const showSeasonsQuery = `
query($id: ID!) {
  tmdbItem(id: $id, type: show) { seasons { number } }
}`

// Episodes looks up a show's regular episodes through riven-ts's TVDB
// indexer, so it works before riven-ts has indexed the show. riven-ts lists a
// show's seasons without their episodes, so a second query asks for each
// season's episodes. A show TVDB does not know has no episodes.
func Episodes(ctx context.Context, client *httpclient.Client, tvdbID string) ([]Episode, error) {
	type seasonsData struct {
		Show *struct {
			Seasons []struct {
				Number int32 `json:"number"`
			} `json:"seasons"`
		} `json:"tmdbItem"`
	}
	d, err := do[seasonsData](ctx, client, showSeasonsQuery, map[string]any{"id": tvdbID})
	if err != nil || d.Show == nil {
		return nil, err
	}

	var numbers []int32
	var fields []string
	for _, season := range d.Show.Seasons {
		if season.Number > 0 {
			numbers = append(numbers, season.Number)
			fields = append(fields, fmt.Sprintf("s%d: season(number: %d) { episodes { number airDate } }", season.Number, season.Number))
		}
	}
	if len(fields) == 0 {
		return nil, nil
	}

	type episodesData struct {
		Show map[string]*struct {
			Episodes []struct {
				Number  int32  `json:"number"`
				AirDate string `json:"airDate"`
			} `json:"episodes"`
		} `json:"tmdbItem"`
	}
	query := "query($id: ID!) {\n  tmdbItem(id: $id, type: show) {\n    " + strings.Join(fields, "\n    ") + "\n  }\n}"
	e, err := do[episodesData](ctx, client, query, map[string]any{"id": tvdbID})
	if err != nil {
		return nil, err
	}
	var episodes []Episode
	for _, number := range numbers {
		season := e.Show[fmt.Sprintf("s%d", number)]
		if season == nil {
			continue
		}
		for _, episode := range season.Episodes {
			// airDate is an ISO timestamp at midnight UTC, e.g. 2025-03-30T00:00:00.000Z.
			airDate, _, _ := strings.Cut(episode.AirDate, "T")
			episodes = append(episodes, Episode{Season: number, Number: episode.Number, AirDate: airDate})
		}
	}
	return episodes, nil
}
