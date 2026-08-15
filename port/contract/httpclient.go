package contract

import (
	"context"

	requestdto "github.com/mihaiflorentin88/ffxiv-census/port/dto/request"
	responsedto "github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

// HTTPClient wraps a small set of outbound HTTP operations.
type HTTPClient interface {
	Do(ctx context.Context, req requestdto.HTTPRequest) (responsedto.HTTPResponse, error)
	Get(ctx context.Context, url string, queryParams, headers map[string]string) (responsedto.HTTPResponse, error)
	Post(ctx context.Context, url string, queryParams, headers map[string]string, body []byte) (responsedto.HTTPResponse, error)
	Patch(ctx context.Context, url string, queryParams, headers map[string]string, body []byte) (responsedto.HTTPResponse, error)
	Delete(ctx context.Context, url string, queryParams, headers map[string]string) (responsedto.HTTPResponse, error)
}
