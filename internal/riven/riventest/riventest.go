// Package riventest is a fake riven-ts GraphQL server for tests. It answers
// with canned responses and records what it receives; it does not imitate
// riven-ts's own logic.
package riventest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Unauthenticated is riven-ts's answer to a request without a valid API key.
const Unauthenticated = `{"data":null,"errors":[{"message":"Access denied! You need to be authenticated to perform this action!","extensions":{"code":"UNAUTHENTICATED"}}]}`

// Call is one GraphQL request the fake received.
type Call struct {
	Query         string
	Variables     map[string]any
	Authorization string
}

// Server is a running fake riven-ts.
type Server struct {
	URL string

	mu        sync.Mutex
	responses map[string]string
	calls     []Call
}

// Start runs a fake riven-ts until the test ends. It answers each POST
// /graphql with the response whose key occurs in the request's query. A query
// no key matches gets a 404, and a query more than one key matches fails the
// test.
func Start(t *testing.T, responses map[string]string) *Server {
	t.Helper()
	s := &Server{responses: responses}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("riventest: decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.calls = append(s.calls, Call{Query: body.Query, Variables: body.Variables, Authorization: r.Header.Get("Authorization")})
		s.mu.Unlock()

		var matched []string
		for key := range s.responses {
			if strings.Contains(body.Query, key) {
				matched = append(matched, key)
			}
		}
		switch len(matched) {
		case 0:
			http.Error(w, "no response matches query: "+body.Query, http.StatusNotFound)
		case 1:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(s.responses[matched[0]]))
		default:
			slices.Sort(matched)
			t.Errorf("riventest: keys %q all match query: %s", matched, body.Query)
			http.Error(w, "ambiguous response keys", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Calls returns the requests received so far, in order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// CallsTo returns the received requests whose query contains substr.
func (s *Server) CallsTo(substr string) []Call {
	var calls []Call
	for _, c := range s.Calls() {
		if strings.Contains(c.Query, substr) {
			calls = append(calls, c)
		}
	}
	return calls
}

// WeeklySeasons answers riven-ts's episode lookup for a show with one
// 12-episode season per start date, numbered from 1, airing one episode a
// week from that date.
func WeeklySeasons(starts ...string) map[string]string {
	var numbers, seasons []string
	for i, start := range starts {
		day, err := time.Parse(time.DateOnly, start)
		if err != nil {
			panic("riventest: bad start date " + start)
		}
		var episodes []string
		for e := range 12 {
			airDate := day.AddDate(0, 0, 7*e).Format(time.DateOnly)
			episodes = append(episodes, `{"number":`+strconv.Itoa(e+1)+`,"airDate":"`+airDate+`T00:00:00.000Z"}`)
		}
		n := strconv.Itoa(i + 1)
		numbers = append(numbers, `{"number":`+n+`}`)
		seasons = append(seasons, `"s`+n+`":{"episodes":[`+strings.Join(episodes, ",")+`]}`)
	}
	return map[string]string{
		"seasons { number }": `{"data":{"tmdbItem":{"seasons":[` + strings.Join(numbers, ",") + `]}}}`,
		"season(number:":     `{"data":{"tmdbItem":{` + strings.Join(seasons, ",") + `}}}`,
	}
}
