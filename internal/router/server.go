package router

import (
	"context"
	"errors"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/httpclient"

	"github.com/evo-de/silo-plugins-request-riven/internal/riven"
)

// Server implements the request_router.v1 RPCs. It is stateless.
type Server struct {
	pluginv1.UnimplementedRequestRouterServer
}

// New returns a ready-to-serve request router.
func New() *Server { return &Server{} }

const requestedBySilo = "silo"

// rivenItem never falls back to IMDb. riven-ts accepts an IMDb-only request
// and then never indexes it. A movie fails with "Item request is missing
// tmdbId", and the TVDB indexer cannot look up a show by IMDb id.
func rivenItem(d *pluginv1.RequestDescriptor) (riven.Item, error) {
	ids := d.GetExternalIds()
	if d.GetMediaType() == "series" {
		if id := ids["tvdb"]; id != "" {
			return riven.Item{Type: riven.Show, ID: id}, nil
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
		status, message := "queued", "requested in riven"
		if err := riven.RequestItem(ctx, riven.NewClient(conn.BaseURL, conn.APIKey), item, requestedBySilo); err != nil {
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

func (s *Server) CheckStatus(ctx context.Context, req *pluginv1.CheckStatusRequest) (*pluginv1.CheckStatusResponse, error) {
	item, err := rivenItem(req.GetRequest())
	if err != nil {
		return &pluginv1.CheckStatusResponse{}, nil
	}

	byID := make(map[string]*httpclient.Client, len(req.GetConnections()))
	for _, c := range req.GetConnections() {
		conn := connectionFromRouter(c)
		byID[conn.ID] = riven.NewClient(conn.BaseURL, conn.APIKey)
	}

	var statuses []*pluginv1.TargetStatus
	for _, tref := range req.GetTargets() {
		client, ok := byID[tref.GetConnectionId()]
		if !ok {
			continue
		}
		state, err := riven.ItemStatus(ctx, client, item)
		if err != nil {
			continue
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

// Validate has no cross-field rules to check: the connection config has no
// fields at all. Returned empty so the host's save-time Validate succeeds.
func (s *Server) Validate(ctx context.Context, req *pluginv1.ValidateRequest) (*pluginv1.ValidateResponse, error) {
	return &pluginv1.ValidateResponse{}, nil
}
