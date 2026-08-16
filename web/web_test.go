package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebUIHandler(t *testing.T) {
	handler := Handler()

	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected text/html content type, got %s", contentType)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "MiX") || !strings.Contains(body, "Internal storage") {
		t.Errorf("HTML body missing expected MiXplorer elements")
	}

	if !strings.Contains(body, "btnToggleView") || !strings.Contains(body, "sortPopover") {
		t.Errorf("HTML body missing interactive components")
	}
}
