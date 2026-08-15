package mockhttpclient

import (
	"context"

	requestdto "github.com/mihaiflorentin88/ffxiv-census/port/dto/request"
	responsedto "github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

// Client is a lightweight test double for the HTTP client contract.
type Client struct {
	DoFunc   func(ctx context.Context, req requestdto.HTTPRequest) (responsedto.HTTPResponse, error)
	Response responsedto.HTTPResponse
	Err      error
	Requests []requestdto.HTTPRequest
}

func (c *Client) Do(ctx context.Context, req requestdto.HTTPRequest) (responsedto.HTTPResponse, error) {
	c.Requests = append(c.Requests, req)
	if c.DoFunc != nil {
		return c.DoFunc(ctx, req)
	}
	return c.Response, c.Err
}

func (c *Client) Get(ctx context.Context, url string, queryParams, headers map[string]string) (responsedto.HTTPResponse, error) {
	return c.Do(ctx, requestdto.HTTPRequest{
		Method:      "GET",
		URL:         url,
		QueryParams: queryParams,
		Headers:     headers,
	})
}

func (c *Client) Post(ctx context.Context, url string, queryParams, headers map[string]string, body []byte) (responsedto.HTTPResponse, error) {
	return c.Do(ctx, requestdto.HTTPRequest{
		Method:      "POST",
		URL:         url,
		QueryParams: queryParams,
		Headers:     headers,
		Body:        body,
	})
}

func (c *Client) Patch(ctx context.Context, url string, queryParams, headers map[string]string, body []byte) (responsedto.HTTPResponse, error) {
	return c.Do(ctx, requestdto.HTTPRequest{
		Method:      "PATCH",
		URL:         url,
		QueryParams: queryParams,
		Headers:     headers,
		Body:        body,
	})
}

func (c *Client) Delete(ctx context.Context, url string, queryParams, headers map[string]string) (responsedto.HTTPResponse, error) {
	return c.Do(ctx, requestdto.HTTPRequest{
		Method:      "DELETE",
		URL:         url,
		QueryParams: queryParams,
		Headers:     headers,
	})
}
