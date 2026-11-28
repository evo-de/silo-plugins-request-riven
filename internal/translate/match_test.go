package translate

import (
	"reflect"
	"testing"
	"time"

	"github.com/evo-de/silo-plugins-request-riven/internal/riven"
)

// weekly returns count episodes of a riven-ts season, one a week from start.
func weekly(season int32, count int, start string) []riven.Episode {
	day, _ := time.Parse(time.DateOnly, start)
	episodes := make([]riven.Episode, count)
	for i := range episodes {
		episodes[i] = riven.Episode{Season: season, Number: int32(i + 1), AirDate: day.AddDate(0, 0, 7*i).Format(time.DateOnly)}
	}
	return episodes
}

func TestMatchSeasonsByAirDate(t *testing.T) {
	// riven-ts splits the year into two 12-episode seasons.
	split := append(weekly(1, 12, "2024-01-07"), weekly(2, 12, "2024-07-07")...)
	// riven-ts keeps 24 weekly episodes in one season.
	merged := weekly(1, 24, "2024-01-07")
	unaired := append(weekly(1, 12, "2024-01-07"), riven.Episode{Season: 2, Number: 1})

	detail := func(number int32, date string, count int32) []siloSeason {
		return []siloSeason{{Number: number, FirstAirDate: date, EpisodeCount: count}}
	}
	cases := []struct {
		name     string
		seasons  []int32
		details  []siloSeason
		episodes []riven.Episode
		want     []int32
	}{
		{"same numbering", []int32{2}, detail(2, "2024-07-07", 12), split, []int32{2}},
		{"season that ends where riven-ts's ends", []int32{1}, detail(1, "2024-01-07", 12), split, []int32{1}},
		{"one season split in two", []int32{1}, detail(1, "2024-01-07", 24), split, []int32{1, 2}},
		{"two seasons merged into one", []int32{2}, detail(2, "2024-03-31", 12), merged, []int32{1}},
		{"fewer episodes than the count", []int32{2}, detail(2, "2024-07-07", 20), split, []int32{2}},
		{"no detail", []int32{3}, nil, split, []int32{3}},
		{"detail without a date", []int32{2}, detail(2, "", 12), merged, []int32{2}},
		{"detail with a count of 0", []int32{2}, detail(2, "2024-03-31", 0), merged, []int32{2}},
		{"no episode on or after the date", []int32{2}, detail(2, "2030-01-01", 12), merged, []int32{2}},
		{"episodes without a date are skipped", []int32{2}, detail(2, "2025-01-01", 8), unaired, []int32{2}},
		{"Specials keep their number", []int32{0}, detail(0, "2024-01-07", 5), merged, []int32{0}},
		{"Specials with a split season", []int32{0, 1}, detail(1, "2024-01-07", 24), split, []int32{0, 1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchSeasons(tc.seasons, tc.details, tc.episodes); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want riven-ts seasons %v, got %v", tc.want, got)
			}
		})
	}
}
