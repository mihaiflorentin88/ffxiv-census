package textproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	requestdto "github.com/mihaiflorentin88/ffxiv-census/port/dto/request"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

type fakeHTTPClient struct {
	responses map[string]response.HTTPResponse
	err       error
}

func (f *fakeHTTPClient) Do(_ context.Context, _ requestdto.HTTPRequest) (response.HTTPResponse, error) {
	return response.HTTPResponse{}, fmt.Errorf("not implemented")
}

func (f *fakeHTTPClient) Get(_ context.Context, url string, _, _ map[string]string) (response.HTTPResponse, error) {
	if f.err != nil {
		return response.HTTPResponse{}, f.err
	}
	if resp, ok := f.responses[url]; ok {
		return resp, nil
	}
	return response.HTTPResponse{StatusCode: 404}, nil
}

func (f *fakeHTTPClient) Post(_ context.Context, _ string, _, _ map[string]string, _ []byte) (response.HTTPResponse, error) {
	return response.HTTPResponse{}, fmt.Errorf("not implemented")
}

func (f *fakeHTTPClient) Patch(_ context.Context, _ string, _, _ map[string]string, _ []byte) (response.HTTPResponse, error) {
	return response.HTTPResponse{}, fmt.Errorf("not implemented")
}

func (f *fakeHTTPClient) Delete(_ context.Context, _ string, _, _ map[string]string) (response.HTTPResponse, error) {
	return response.HTTPResponse{}, fmt.Errorf("not implemented")
}

func (f *fakeHTTPClient) GetStream(_ context.Context, url string, _, _ map[string]string, consume func(int, io.Reader) error) error {
	if f.err != nil {
		return f.err
	}
	if resp, ok := f.responses[url]; ok {
		return consume(resp.StatusCode, bytes.NewReader(resp.Body))
	}
	return consume(404, bytes.NewReader(nil))
}

func TestFetchProxies_Success(t *testing.T) {
	httpURL := "http://test/http.txt"
	socks4URL := "http://test/socks4.txt"

	client := New(&fakeHTTPClient{
		responses: map[string]response.HTTPResponse{
			httpURL:   {StatusCode: 200, Body: []byte("1.2.3.4:8080\n5.6.7.8:1080\n9.10.11.12:4145\n")},
			socks4URL: {StatusCode: 200, Body: []byte("1.2.3.4:8080\n5.6.7.8:1080\n9.10.11.12:4145\n")},
		},
	}, "test", map[string]string{
		"http":   httpURL,
		"socks4": socks4URL,
	})

	var records []contract.ProxyRecord
	err := client.FetchProxies(context.Background(), func(rec contract.ProxyRecord) error {
		records = append(records, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchProxies: %v", err)
	}

	if len(records) != 6 {
		t.Fatalf("expected 6 records, got %d", len(records))
	}

	if records[0].IP != "1.2.3.4" {
		t.Errorf("expected IP 1.2.3.4, got %s", records[0].IP)
	}
	if records[0].Port != 8080 {
		t.Errorf("expected port 8080, got %d", records[0].Port)
	}
	if records[0].Source != "test" {
		t.Errorf("expected source test, got %s", records[0].Source)
	}
	if records[0].Status != contract.ProxyStatusInactive {
		t.Errorf("expected status inactive, got %s", records[0].Status)
	}
}

func TestFetchProxies_SkipsMalformedLines(t *testing.T) {
	body := "1.2.3.4:8080\nbad-line\n5.6.7.8:1080\n\n"
	client := New(&fakeHTTPClient{
		responses: map[string]response.HTTPResponse{
			"http://test/http.txt": {StatusCode: 200, Body: []byte(body)},
		},
	}, "test", map[string]string{
		"http": "http://test/http.txt",
	})

	var records []contract.ProxyRecord
	err := client.FetchProxies(context.Background(), func(rec contract.ProxyRecord) error {
		records = append(records, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchProxies: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
}

func TestFetchProxies_PartialFailure(t *testing.T) {
	client := New(&fakeHTTPClient{
		responses: map[string]response.HTTPResponse{
			"http://test/http.txt":   {StatusCode: 200, Body: []byte("1.2.3.4:8080\n")},
			"http://test/socks5.txt": {StatusCode: 500},
		},
	}, "test", map[string]string{
		"http":   "http://test/http.txt",
		"socks5": "http://test/socks5.txt",
	})

	var records []contract.ProxyRecord
	err := client.FetchProxies(context.Background(), func(rec contract.ProxyRecord) error {
		records = append(records, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchProxies: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Protocol != "http" {
		t.Errorf("expected protocol http, got %s", records[0].Protocol)
	}
}

func TestFetchProxies_AllFail(t *testing.T) {
	client := New(&fakeHTTPClient{
		responses: map[string]response.HTTPResponse{
			"http://test/http.txt": {StatusCode: 500},
		},
	}, "test", map[string]string{
		"http": "http://test/http.txt",
	})

	err := client.FetchProxies(context.Background(), func(_ contract.ProxyRecord) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error when all sources fail")
	}
}

func TestFetchProxies_StripsProtocolPrefix(t *testing.T) {
	body := "http://1.2.3.4:8080\nsocks4://5.6.7.8:1080\nsocks5://9.10.11.12:4145\n"
	client := New(&fakeHTTPClient{
		responses: map[string]response.HTTPResponse{
			"http://test/http.txt": {StatusCode: 200, Body: []byte(body)},
		},
	}, "test", map[string]string{
		"http": "http://test/http.txt",
	})

	var records []contract.ProxyRecord
	err := client.FetchProxies(context.Background(), func(rec contract.ProxyRecord) error {
		records = append(records, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchProxies: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	if records[0].IP != "1.2.3.4" || records[0].Port != 8080 {
		t.Errorf("expected 1.2.3.4:8080, got %s:%d", records[0].IP, records[0].Port)
	}
	if records[1].IP != "5.6.7.8" || records[1].Port != 1080 {
		t.Errorf("expected 5.6.7.8:1080, got %s:%d", records[1].IP, records[1].Port)
	}
}

func TestName(t *testing.T) {
	client := New(nil, "proxifly", nil)
	if client.Name() != "proxifly" {
		t.Errorf("expected name proxifly, got %s", client.Name())
	}
}
