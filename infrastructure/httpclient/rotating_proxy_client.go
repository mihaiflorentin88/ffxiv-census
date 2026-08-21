package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	requestdto "github.com/mihaiflorentin88/ffxiv-census/port/dto/request"
	responsedto "github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

// retryableStatusError is returned by the rotating client's inner callback when
// the upstream HTTP status indicates the proxy should be swapped (403, 429, 5xx).
// The rotating loop distinguishes this from parser/emit errors which must not rotate.
type retryableStatusError struct {
	status int
}

func (e retryableStatusError) Error() string {
	return fmt.Sprintf("retryable HTTP status %d", e.status)
}

// transportError wraps errors from the HTTP transport layer (DNS, dial, TLS,
// timeout, connection refused) or from NewProxyClient construction. These
// errors indicate the proxy itself is unreachable and should trigger rotation,
// unlike consume/parse errors which must propagate immediately.
type transportError struct {
	err error
}

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// RotatingProxyClient implements contract.HTTPClient by routing GET requests
// through a rotating pool of unlocked proxies. On retryable failures (403,
// 429, 5xx) it swaps to a different proxy and retries up to maxAttempts times.
// When no proxy is available it falls back to a direct (non-proxied) request.
type RotatingProxyClient struct {
	hub         *proxy.ProxyHub
	direct      contract.HTTPClient
	timeout     time.Duration
	maxAttempts int
}

// NewRotatingProxyClient creates a RotatingProxyClient. The direct client is
// used for the fallback path and as the base for proxied requests.
func NewRotatingProxyClient(hub *proxy.ProxyHub, direct contract.HTTPClient, timeout time.Duration) *RotatingProxyClient {
	return &RotatingProxyClient{
		hub:         hub,
		direct:      direct,
		timeout:     timeout,
		maxAttempts: 3,
	}
}

func (c *RotatingProxyClient) GetStream(
	ctx context.Context,
	rawURL string,
	queryParams, headers map[string]string,
	consume func(int, io.Reader) error,
) error {
	if consume == nil {
		return fmt.Errorf("stream response consumer is required")
	}

	var attemptedIDs []int64
	current, err := c.hub.RandomActive(ctx)
	if err != nil {
		return err
	}
	if current == nil {
		return c.direct.GetStream(ctx, rawURL, queryParams, headers, consume)
	}
	attemptedIDs = append(attemptedIDs, current.Record().ID)

	var lastErr error
	for attempt := 0; attempt < c.maxAttempts && current != nil; attempt++ {
		proxied, pErr := NewProxyClient(current.Address(), c.timeout)
		if pErr != nil {
			lastErr = &transportError{err: pErr}
			current, err = c.hub.RandomActiveExcluding(ctx, attemptedIDs)
			if err != nil {
				return errors.Join(lastErr, err)
			}
			if current != nil {
				attemptedIDs = append(attemptedIDs, current.Record().ID)
			}
			continue
		}
		pErr = proxied.GetStream(ctx, rawURL, queryParams, headers, func(status int, body io.Reader) error {
			if status == http.StatusForbidden || status == http.StatusTooManyRequests || status >= 500 {
				return retryableStatusError{status: status}
			}
			return consume(status, body)
		})
		if pErr == nil {
			return nil
		}
		// Consume/parse errors propagate immediately without rotating.
		var rse retryableStatusError
		if !errors.As(pErr, &rse) {
			// Also check if it's a transport error from the underlying client.
			var te *transportError
			if !errors.As(pErr, &te) {
				return pErr
			}
		}
		lastErr = pErr
		current, err = c.hub.RandomActiveExcluding(ctx, attemptedIDs)
		if err != nil {
			return errors.Join(lastErr, err)
		}
		if current != nil {
			attemptedIDs = append(attemptedIDs, current.Record().ID)
		}
	}
	return lastErr
}

// Do, Get, Post, Patch, Delete delegate to the direct client.
func (c *RotatingProxyClient) Do(ctx context.Context, req requestdto.HTTPRequest) (responsedto.HTTPResponse, error) {
	return c.direct.Do(ctx, req)
}

func (c *RotatingProxyClient) Get(ctx context.Context, url string, queryParams, headers map[string]string) (responsedto.HTTPResponse, error) {
	return c.direct.Get(ctx, url, queryParams, headers)
}

func (c *RotatingProxyClient) Post(ctx context.Context, url string, queryParams, headers map[string]string, body []byte) (responsedto.HTTPResponse, error) {
	return c.direct.Post(ctx, url, queryParams, headers, body)
}

func (c *RotatingProxyClient) Patch(ctx context.Context, url string, queryParams, headers map[string]string, body []byte) (responsedto.HTTPResponse, error) {
	return c.direct.Patch(ctx, url, queryParams, headers, body)
}

func (c *RotatingProxyClient) Delete(ctx context.Context, url string, queryParams, headers map[string]string) (responsedto.HTTPResponse, error) {
	return c.direct.Delete(ctx, url, queryParams, headers)
}
