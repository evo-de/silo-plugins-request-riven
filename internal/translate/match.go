package translate

import (
	"cmp"
	"slices"
	"strings"

	"github.com/evo-de/silo-plugins-request-riven/internal/riven"
)

// siloSeason is what season matching needs to know about a Silo season.
type siloSeason struct {
	Number int32
	// FirstAirDate is YYYY-MM-DD, or empty when unknown.
	FirstAirDate string
	// EpisodeCount is 0 when unknown.
	EpisodeCount int32
}

// usableDetail returns the detail for a regular Silo season, or nil when there
// is none or it has no date or no episode count.
func usableDetail(details []siloSeason, number int32) *siloSeason {
	if number == 0 {
		return nil
	}
	for i, d := range details {
		if d.Number == number && d.FirstAirDate != "" && d.EpisodeCount > 0 {
			return &details[i]
		}
	}
	return nil
}

// matchSeasons finds each requested Silo season the way Silo's request
// contract describes. The season is the first EpisodeCount regular episodes,
// in airing order, that air on or after its first air date, or the ones that
// do when fewer match. riven-ts fetches whole seasons, so the result is every
// riven-ts season those episodes belong to. Specials, a season without a
// usable detail, and a season no episode matches keep their number.
func matchSeasons(seasons []int32, details []siloSeason, episodes []riven.Episode) []int32 {
	aired := slices.DeleteFunc(slices.Clone(episodes), func(e riven.Episode) bool { return e.AirDate == "" })
	slices.SortFunc(aired, func(a, b riven.Episode) int {
		return cmp.Or(strings.Compare(a.AirDate, b.AirDate), cmp.Compare(a.Season, b.Season), cmp.Compare(a.Number, b.Number))
	})

	var out []int32
	for _, number := range seasons {
		var matched []riven.Episode
		if d := usableDetail(details, number); d != nil {
			start := slices.IndexFunc(aired, func(e riven.Episode) bool { return e.AirDate >= d.FirstAirDate })
			if start >= 0 {
				matched = aired[start:min(start+int(d.EpisodeCount), len(aired))]
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
