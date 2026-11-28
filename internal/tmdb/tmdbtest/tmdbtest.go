// Package tmdbtest is a fake TMDB API for tests. It answers every request
// with one canned response and records what it receives.
package tmdbtest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
)

// Request is one request the fake received.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
}

// Server is a running fake TMDB.
type Server struct {
	URL string

	mu       sync.Mutex
	requests []Request
}

// Start runs a fake TMDB until the test ends. It answers every request with
// status and body.
func Start(t *testing.T, status int, body string) *Server {
	t.Helper()
	s := &Server{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone()})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Requests returns the requests received so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Paths returns the path of each request received so far, in order.
func (s *Server) Paths() []string {
	var paths []string
	for _, r := range s.Requests() {
		paths = append(paths, r.Path)
	}
	return paths
}
