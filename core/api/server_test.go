package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/tests/dbutil"
)

func TestHealthz(t *testing.T) {
	s := New(false)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyzWithoutPoolIsUnavailable(t *testing.T) {
	s := New(false)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.Echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadyzWithPool(t *testing.T) {
	dsn := dbutil.DSN(t)
	pool, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	s := New(false)
	s.AttachPool(pool)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.Echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
}
