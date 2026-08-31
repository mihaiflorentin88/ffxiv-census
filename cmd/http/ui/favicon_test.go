package ui

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// TestFaviconServed verifies the conventional favicon path serves the
// embedded PNG icon with an image content type.
func TestFaviconServed(t *testing.T) {
	rig := newTestRig(t)
	mux := http.NewServeMux()
	rig.ctrl.RegisterRoutes(mux)

	rec := serveMuxGet(t, mux, "/favicon.ico")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("expected Content-Type image/png, got %q", ct)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("expected PNG bytes at /favicon.ico, got %d bytes", rec.Body.Len())
	}
}

// TestFaviconLinkedInHead verifies indexable pages link the icon from the
// document head so search results can display it.
func TestFaviconLinkedInHead(t *testing.T) {
	rig := newTestRig(t)
	mux := http.NewServeMux()
	rig.ctrl.RegisterRoutes(mux)

	rec := serveMuxGet(t, mux, "/ui/dashboard")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<link rel=\"icon\"") || !strings.Contains(body, "href=\"/favicon.ico\"") {
		t.Fatalf("favicon link missing from head:\n%s", body)
	}
}
