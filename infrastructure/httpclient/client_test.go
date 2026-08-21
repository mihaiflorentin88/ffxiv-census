package httpclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// countingRT is a RoundTripper that counts how many requests are made.
type countingRT struct {
	count int32
}

func (r *countingRT) RoundTrip(*http.Request) (*http.Response, error) {
	atomic.AddInt32(&r.count, 1)
	return nil, errors.New("nope")
}

func TestClient_GetStream_NilConsumer(t *testing.T) {
	rt := &countingRT{}
	c := New(&http.Client{Transport: rt})

	err := c.GetStream(context.Background(), "http://example.com", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil consumer")
	}
	if !strings.Contains(err.Error(), "consumer is required") {
		t.Errorf("error %q does not contain 'consumer is required'", err.Error())
	}
	if n := atomic.LoadInt32(&rt.count); n != 0 {
		t.Errorf("expected 0 HTTP requests, got %d", n)
	}
}
