package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/PithomLabs/a1/internal/apperr"
)

func testCtx(method, target string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func TestResolveRequiresOperatorOnMutations(t *testing.T) {
	c, _ := testCtx(http.MethodPost, "/api/v1/employers/E/employees")
	if _, err := resolve(c, nil, false); err == nil {
		t.Fatal("expected validation error for mutation without X-Operator")
	}
	c, _ = testCtx(http.MethodPatch, "/api/v1/employers/E/employees/1")
	if _, err := resolve(c, nil, false); err == nil {
		t.Fatal("expected validation error for PATCH without X-Operator")
	}
	c, _ = testCtx(http.MethodPost, "/api/v1/employers/E/employees")
	c.Request().Header.Set("X-Operator", "op-1")
	h, err := resolve(c, nil, false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if h.operator != "op-1" {
		t.Fatalf("operator=%q", h.operator)
	}
	c, _ = testCtx(http.MethodGet, "/api/v1/employers/E/employees")
	h, err = resolve(c, nil, false)
	if err != nil {
		t.Fatalf("reads must not require an operator: %v", err)
	}
	if h.operator == "" {
		t.Fatal("reads expect a default operator label")
	}
}

func TestFailEnvelope(t *testing.T) {
	c, rec := testCtx(http.MethodGet, "/x")
	c.Request().Header.Set(echo.HeaderXRequestID, "req-123")
	if err := fail(c, apperr.New(apperr.CodeValidation, "bad input")); err != nil {
		t.Fatalf("fail: %v", err)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != "validation" || body["message"] != "bad input" || body["request_id"] != "req-123" {
		t.Fatalf("envelope=%v", body)
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", rec.Code)
	}
	// Raw errors never leak internals to the client.
	c2, rec2 := testCtx(http.MethodGet, "/x")
	if err := fail(c2, errors.New("pq: column secret leaked")); err != nil {
		t.Fatalf("fail: %v", err)
	}
	var body2 map[string]string
	if err := json.Unmarshal(rec2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body2["error"] != "internal" || body2["message"] != "request failed" {
		t.Fatalf("envelope=%v", body2)
	}
}
