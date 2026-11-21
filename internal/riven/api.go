package riven

import (
	"context"
	"fmt"
	"net/http"
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
	_, err := do[struct{}](ctx, client, requestItemMutation, map[string]any{"input": input})
	return err
}

// itemStatusQuery leaves out externalRequestId: riven-ts stores it as null
// despite its String! type, and selecting it nulls the whole result.
const itemStatusQuery = `
query($tmdbId: String, $tvdbId: String) {
  itemRequestByExternalIds(tmdbId: $tmdbId, tvdbId: $tvdbId) {
    state
    primaryMediaItem {
      ... on Movie { state }
      ... on Show { state }
    }
  }
}`

// ItemStatus passes only the item's own id, because riven-ts matches a request
// against every id it is given.
func ItemStatus(ctx context.Context, client *httpclient.Client, item Item) (ItemState, error) {
	type data struct {
		Request struct {
			State            string `json:"state"`
			PrimaryMediaItem struct {
				State string `json:"state"`
			} `json:"primaryMediaItem"`
		} `json:"itemRequestByExternalIds"`
	}
	d, err := do[data](ctx, client, itemStatusQuery, map[string]any{item.idField(): item.ID})
	if err != nil {
		return ItemState{}, err
	}
	return ItemState{Request: d.Request.State, Media: d.Request.PrimaryMediaItem.State}, nil
}
