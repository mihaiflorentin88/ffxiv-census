package lodestone

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/mock"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// TestDoRequest_ResponseTimeoutReturnsImmediately pins the fast-fail
// semantics for a full response timeout: once the proxy tunnel is
// established and Lodestone does not answer within the client timeout,
// retrying through the same proxy after a sub-second backoff cannot clear
// the stall. The delivery must return to the queue immediately so the
// retry ladder redelivers it — usually through a different proxy — instead
// of the goroutine babysitting the request for minutes (the unacked window
// that showed up as 1m-2m43s jobs). A response timeout is target-side
// ambiguity, not a proven proxy death, so it must not be classified as
// CheckProxy either.
func TestDoRequest_ResponseTimeoutReturnsImmediately(t *testing.T) {
	requests := 0
	c := testCustomClientWithLimiter(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, &url.Error{
			Op:  "Get",
			URL: "https://na.finalfantasyxiv.com/lodestone/character/1/",
			Err: context.DeadlineExceeded,
		}
	}), mock.NewProviderRateLimiter(), 3)

	_, _, err := c.doRequest(context.Background(), "https://na.finalfantasyxiv.com/lodestone/character/1/")
	if err == nil {
		t.Fatal("expected the timeout error to surface")
	}
	if requests != 1 {
		t.Fatalf("a response timeout must abort the in-client ladder after 1 attempt, got %d requests", requests)
	}
	var checkErr *contract.ProxyCheckError
	if errors.As(err, &checkErr) && checkErr.Kind == contract.CheckProxy {
		t.Fatal("a response timeout is target-side ambiguity and must not rotate the proxy")
	}
}
