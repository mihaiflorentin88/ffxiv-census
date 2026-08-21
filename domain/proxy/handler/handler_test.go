package handler_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	proxydomain "github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/handler"
	"github.com/mihaiflorentin88/ffxiv-census/mock/repository"
)

func TestNewProxy_Handle_BadPayload(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	svc := proxydomain.NewService(nil, repo, nil, nil, 48*time.Hour, 5)
	h := handler.NewNewProxy(svc, nil)

	_, err := h.Handle(context.Background(), []byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for bad payload")
	}
}

func TestNewProxyJob_Serialization(t *testing.T) {
	country := "US"
	job := handler.NewProxyJob(handler.NewProxyPayload{
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Country:  &country,
		Source:   "test",
	})

	if job.Type != handler.EventNewProxy {
		t.Fatalf("expected type %s, got %s", handler.EventNewProxy, job.Type)
	}

	var p handler.NewProxyPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.IP != "1.2.3.4" || p.Port != 8080 || p.Country == nil || *p.Country != "US" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}
