package httpclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	requestdto "github.com/mihaiflorentin88/ffxiv-census/port/dto/request"
	responsedto "github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

const defaultTimeout = 30 * time.Second

type client struct {
	httpClient *http.Client
}

// New constructs an HTTP client adapter with a sensible default timeout.
func New(httpClient *http.Client) contract.HTTPClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &client{httpClient: httpClient}
}

func (c *client) Do(ctx context.Context, req requestdto.HTTPRequest) (responsedto.HTTPResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		return responsedto.HTTPResponse{}, fmt.Errorf("http method is required")
	}
	if strings.TrimSpace(req.URL) == "" {
		return responsedto.HTTPResponse{}, fmt.Errorf("request url is required")
	}

	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return responsedto.HTTPResponse{}, fmt.Errorf("parse url %q: %w", req.URL, err)
	}

	query := parsedURL.Query()
	for key, value := range req.QueryParams {
		query.Set(key, value)
	}
	parsedURL.RawQuery = query.Encode()

	requestCtx := ctx
	var cancel context.CancelFunc = func() {}
	if req.Timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	httpReq, err := http.NewRequestWithContext(requestCtx, method, parsedURL.String(), bytes.NewReader(req.Body))
	if err != nil {
		return responsedto.HTTPResponse{}, fmt.Errorf("build request %s %s: %w", method, parsedURL.String(), err)
	}
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return responsedto.HTTPResponse{}, fmt.Errorf("execute request %s %s: %w", method, parsedURL.String(), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return responsedto.HTTPResponse{}, fmt.Errorf("read response body %s %s: %w", method, parsedURL.String(), err)
	}

	response := responsedto.HTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    cloneHeaders(resp.Header),
		Body:       body,
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return response, fmt.Errorf("request %s %s failed with status %d: %s", method, parsedURL.String(), resp.StatusCode, message)
	}

	return response, nil
}

func (c *client) Get(ctx context.Context, url string, queryParams, headers map[string]string) (responsedto.HTTPResponse, error) {
	return c.Do(ctx, requestdto.HTTPRequest{
		Method:      http.MethodGet,
		URL:         url,
		QueryParams: queryParams,
		Headers:     headers,
	})
}

func (c *client) Post(ctx context.Context, url string, queryParams, headers map[string]string, body []byte) (responsedto.HTTPResponse, error) {
	return c.Do(ctx, requestdto.HTTPRequest{
		Method:      http.MethodPost,
		URL:         url,
		QueryParams: queryParams,
		Headers:     headers,
		Body:        body,
	})
}

func (c *client) Patch(ctx context.Context, url string, queryParams, headers map[string]string, body []byte) (responsedto.HTTPResponse, error) {
	return c.Do(ctx, requestdto.HTTPRequest{
		Method:      http.MethodPatch,
		URL:         url,
		QueryParams: queryParams,
		Headers:     headers,
		Body:        body,
	})
}

func (c *client) Delete(ctx context.Context, url string, queryParams, headers map[string]string) (responsedto.HTTPResponse, error) {
	return c.Do(ctx, requestdto.HTTPRequest{
		Method:      http.MethodDelete,
		URL:         url,
		QueryParams: queryParams,
		Headers:     headers,
	})
}

func cloneHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return map[string][]string{}
	}

	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		copyValues := make([]string, len(values))
		copy(copyValues, values)
		cloned[key] = copyValues
	}
	return cloned
}
