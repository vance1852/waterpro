package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/auth"
	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/incident"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
	"github.com/vance1852/waterpro/internal/sampling"
	"github.com/vance1852/waterpro/internal/source"
	"github.com/vance1852/waterpro/internal/telemetry"
)

type httpFixture struct {
	store   *repository.Store
	handler http.Handler
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	store, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "http.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now().UTC()
	if err := store.CreateOrganization(context.Background(), "org-1", "HTTP Authority", now); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(context.Background(), domain.User{ID: "supervisor", OrganizationID: "org-1", Email: "supervisor@example.test", PasswordHash: hash, Role: domain.RoleProtectionSupervisor, Active: true, AuthGeneration: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(store, time.Hour)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(store, authService, source.NewService(store), sampling.NewService(store), incident.NewService(store, time.Minute), telemetry.NewService(store), logger).Handler()
	return &httpFixture{store: store, handler: handler}
}

func perform(handler http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		payload, _ := json.Marshal(body)
		reader = bytes.NewReader(payload)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func loginHTTP(t *testing.T, fixture *httpFixture) string {
	t.Helper()
	response := perform(fixture.handler, http.MethodPost, "/v1/auth/login", map[string]any{"organization_id": "org-1", "email": "supervisor@example.test", "password": "correct-horse-battery-staple"}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Token == "" {
		t.Fatal("empty login token")
	}
	return result.Token
}

func TestHealthAndReadiness(t *testing.T) {
	fixture := newHTTPFixture(t)
	health := perform(fixture.handler, http.MethodGet, "/healthz", nil, "")
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), "alive") {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
	ready := perform(fixture.handler, http.MethodGet, "/readyz", nil, "")
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), "ready") {
		t.Fatalf("ready status=%d body=%s", ready.Code, ready.Body.String())
	}
	if ready.Header().Get("X-Request-ID") == "" {
		t.Fatal("readiness response lacks request id")
	}
}

func TestProtectedRouteRequiresBearerSession(t *testing.T) {
	fixture := newHTTPFixture(t)
	missing := perform(fixture.handler, http.MethodGet, "/v1/sources", nil, "")
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d", missing.Code)
	}
	invalid := perform(fixture.handler, http.MethodGet, "/v1/sources", nil, "not-a-session")
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("invalid auth status=%d", invalid.Code)
	}
	token := loginHTTP(t, fixture)
	valid := perform(fixture.handler, http.MethodGet, "/v1/sources", nil, token)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid auth status=%d body=%s", valid.Code, valid.Body.String())
	}
}

func TestRegisterAndListWaterSource(t *testing.T) {
	fixture := newHTTPFixture(t)
	token := loginHTTP(t, fixture)
	created := perform(fixture.handler, http.MethodPost, "/v1/sources", map[string]any{"name": "HTTP Reservoir", "kind": "reservoir", "timezone": "UTC"}, token)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var sourceResult domain.WaterSource
	if err := json.Unmarshal(created.Body.Bytes(), &sourceResult); err != nil {
		t.Fatal(err)
	}
	if sourceResult.ID == "" || sourceResult.OrganizationID != "org-1" {
		t.Fatalf("source = %#v", sourceResult)
	}
	listed := perform(fixture.handler, http.MethodGet, "/v1/sources?limit=10", nil, token)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	if !strings.Contains(listed.Body.String(), sourceResult.ID) {
		t.Fatalf("list does not contain source: %s", listed.Body.String())
	}
}

func TestUnknownJSONFieldIsRejected(t *testing.T) {
	fixture := newHTTPFixture(t)
	token := loginHTTP(t, fixture)
	response := perform(fixture.handler, http.MethodPost, "/v1/sources", map[string]any{"name": "Bad Reservoir", "kind": "reservoir", "timezone": "UTC", "unknown": true}, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "invalid_json") {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestRequestIDIsPreservedWhenValid(t *testing.T) {
	fixture := newHTTPFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-ID", "external-request-123")
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if got := response.Header().Get("X-Request-ID"); got != "external-request-123" {
		t.Fatalf("request id = %q", got)
	}
}

func TestLogoutRevokesHTTPToken(t *testing.T) {
	fixture := newHTTPFixture(t)
	token := loginHTTP(t, fixture)
	logout := perform(fixture.handler, http.MethodPost, "/v1/auth/logout", nil, token)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	after := perform(fixture.handler, http.MethodGet, "/v1/sources", nil, token)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d", after.Code)
	}
}

func TestClassifyErrorMapsStableHTTPContracts(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrValidation, http.StatusBadRequest, "validation"},
		{domain.ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
		{domain.ErrForbidden, http.StatusForbidden, "forbidden"},
		{domain.ErrNotFound, http.StatusNotFound, "not_found"},
		{domain.ErrConflict, http.StatusConflict, "conflict"},
		{domain.ErrInvalidTransition, http.StatusConflict, "conflict"},
		{domain.ErrLeaseLost, http.StatusConflict, "conflict"},
		{domain.ErrCapacityExceeded, http.StatusUnprocessableEntity, "capacity_exceeded"},
		{context.DeadlineExceeded, http.StatusGatewayTimeout, "deadline"},
		{errors.New("database failed"), http.StatusInternalServerError, "internal"},
	}
	for _, test := range tests {
		status, code, message := classifyError(test.err)
		if status != test.status || code != test.code || message == "" {
			t.Errorf("classify(%v) = %d %s %q", test.err, status, code, message)
		}
	}
}

func TestInvalidLoginDoesNotExposeCredentialDetails(t *testing.T) {
	fixture := newHTTPFixture(t)
	response := perform(fixture.handler, http.MethodPost, "/v1/auth/login", map[string]any{"organization_id": "org-1", "email": "supervisor@example.test", "password": "wrong-password-value"}, "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "bcrypt") || strings.Contains(body, "password hash") {
		t.Fatalf("credential detail leaked: %s", body)
	}
}
