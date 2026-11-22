package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const BaseURL = "https://api.themoviedb.org/3"

// Season is one season of a show, numbered the way TMDB and Silo number it.
type Season struct {
	Number int32
	// AirDate is YYYY-MM-DD, or empty when TMDB has no date for the season.
	AirDate      string
	EpisodeCount int32
}

// Seasons returns the seasons TMDB lists for a show. key is either a v4 API
// Read Access Token, which is a JWT and so contains dots, or a v3 API key.
func Seasons(ctx context.Context, baseURL, key, showID string) ([]Season, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/tv/"+url.PathEscape(showID), nil)
	if err != nil {
		return nil, fmt.Errorf("tmdb: %w", err)
	}
	if strings.Contains(key, ".") {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.URL.RawQuery = url.Values{"api_key": {key}}.Encode()
	}

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		// The *url.Error names the URL, which holds a v3 key, and Silo shows
		// the message to administrators.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("tmdb: look up show %s: %w", showID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("tmdb: look up show %s: %s", showID, resp.Status)
	}

	var show struct {
		Seasons []struct {
			SeasonNumber int32  `json:"season_number"`
			AirDate      string `json:"air_date"`
			EpisodeCount int32  `json:"episode_count"`
		} `json:"seasons"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
		return nil, fmt.Errorf("tmdb: decode show %s: %w", showID, err)
	}
	seasons := make([]Season, 0, len(show.Seasons))
	for _, s := range show.Seasons {
		seasons = append(seasons, Season{Number: s.SeasonNumber, AirDate: s.AirDate, EpisodeCount: s.EpisodeCount})
	}
	return seasons, nil
}
