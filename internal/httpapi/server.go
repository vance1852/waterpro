package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vance1852/waterpro/internal/auth"
	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/incident"
	"github.com/vance1852/waterpro/internal/middleware"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
	"github.com/vance1852/waterpro/internal/sampling"
	"github.com/vance1852/waterpro/internal/source"
	"github.com/vance1852/waterpro/internal/telemetry"
)

type Server struct {
	store     *repository.Store
	auth      *auth.Service
	sources   *source.Service
	sampling  *sampling.Service
	incidents *incident.Service
	telemetry *telemetry.Service
	logger    *slog.Logger
}

func New(store *repository.Store, authService *auth.Service, sources *source.Service, samples *sampling.Service, incidents *incident.Service, telemetryService *telemetry.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{store: store, auth: authService, sources: sources, sampling: samples, incidents: incidents, telemetry: telemetryService, logger: logger}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("POST /v1/auth/login", s.login)
	mux.Handle("POST /v1/auth/logout", middleware.RequireAuth(s.auth, http.HandlerFunc(s.logout)))
	mux.Handle("GET /v1/sources", middleware.RequireAuth(s.auth, http.HandlerFunc(s.listSources)))
	mux.Handle("POST /v1/sources", middleware.RequireAuth(s.auth, http.HandlerFunc(s.registerSource)))
	mux.Handle("POST /v1/sampling/plans", middleware.RequireAuth(s.auth, http.HandlerFunc(s.createSamplingPlan)))
	mux.Handle("POST /v1/samples/collect", middleware.RequireAuth(s.auth, http.HandlerFunc(s.collectSample)))
	mux.Handle("POST /v1/incidents", middleware.RequireAuth(s.auth, http.HandlerFunc(s.reportIncident)))
	mux.Handle("POST /v1/incidents/{id}/claim", middleware.RequireAuth(s.auth, http.HandlerFunc(s.claimIncident)))
	mux.Handle("POST /v1/telemetry/readings", middleware.RequireAuth(s.auth, http.HandlerFunc(s.ingestTelemetry)))
	return middleware.Recovery(s.logger, middleware.RequestID(middleware.AccessLog(s.logger, mux)))
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "alive"})
}

func (s *Server) ready(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) login(response http.ResponseWriter, request *http.Request) {
	var command auth.LoginCommand
	if !decodeJSON(response, request, &command) {
		return
	}
	result, err := s.auth.Login(request.Context(), command)
	if err != nil {
		s.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) logout(response http.ResponseWriter, request *http.Request) {
	parts := strings.Fields(request.Header.Get("Authorization"))
	if len(parts) != 2 {
		s.writeError(response, request, domain.ErrUnauthorized)
		return
	}
	if err := s.auth.Logout(request.Context(), parts[1]); err != nil {
		s.writeError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSources(response http.ResponseWriter, request *http.Request) {
	actor, _ := middleware.ActorFrom(request.Context())
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	page, err := s.sources.ListSources(request.Context(), actor, request.URL.Query().Get("active") != "false", domain.PageRequest{Limit: limit, Cursor: request.URL.Query().Get("cursor")})
	if err != nil {
		s.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, page)
}

func (s *Server) registerSource(response http.ResponseWriter, request *http.Request) {
	actor, _ := middleware.ActorFrom(request.Context())
	var command source.RegisterSourceCommand
	if !decodeJSON(response, request, &command) {
		return
	}
	command.RequestID = middleware.RequestIDFrom(request.Context())
	created, err := s.sources.RegisterWaterSource(request.Context(), actor, command)
	if err != nil {
		s.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) createSamplingPlan(response http.ResponseWriter, request *http.Request) {
	actor, _ := middleware.ActorFrom(request.Context())
	var command sampling.CreatePlanCommand
	if !decodeJSON(response, request, &command) {
		return
	}
	command.RequestID = middleware.RequestIDFrom(request.Context())
	created, err := s.sampling.CreatePlan(request.Context(), actor, command)
	if err != nil {
		s.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) collectSample(response http.ResponseWriter, request *http.Request) {
	actor, _ := middleware.ActorFrom(request.Context())
	var command sampling.CollectCommand
	if !decodeJSON(response, request, &command) {
		return
	}
	command.RequestID = middleware.RequestIDFrom(request.Context())
	created, err := s.sampling.Collect(request.Context(), actor, command)
	if err != nil {
		s.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) reportIncident(response http.ResponseWriter, request *http.Request) {
	actor, _ := middleware.ActorFrom(request.Context())
	var command incident.ReportCommand
	if !decodeJSON(response, request, &command) {
		return
	}
	command.RequestID = middleware.RequestIDFrom(request.Context())
	created, err := s.incidents.Report(request.Context(), actor, command)
	if err != nil {
		s.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) claimIncident(response http.ResponseWriter, request *http.Request) {
	actor, _ := middleware.ActorFrom(request.Context())
	claimed, err := s.incidents.Claim(request.Context(), actor, request.PathValue("id"))
	if err != nil {
		s.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, claimed)
}

func (s *Server) ingestTelemetry(response http.ResponseWriter, request *http.Request) {
	actor, _ := middleware.ActorFrom(request.Context())
	var command telemetry.IngestCommand
	if !decodeJSON(response, request, &command) {
		return
	}
	command.RequestID = middleware.RequestIDFrom(request.Context())
	reading, created, err := s.telemetry.Ingest(request.Context(), actor, command)
	if err != nil {
		s.writeError(response, request, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(response, status, reading)
}

func (s *Server) writeError(response http.ResponseWriter, request *http.Request, err error) {
	status, code, message := classifyError(err)
	if status >= 500 {
		s.logger.ErrorContext(request.Context(), "request failed", "error", err, "request_id", middleware.RequestIDFrom(request.Context()))
	}
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": middleware.RequestIDFrom(request.Context())}})
}

func classifyError(err error) (int, string, string) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "validation", "request validation failed"
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "authentication required"
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "forbidden", "operation is not permitted"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found", "requested resource was not found"
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrLeaseLost):
		return http.StatusConflict, "conflict", "resource state conflicts with the operation"
	case errors.Is(err, domain.ErrCapacityExceeded):
		return http.StatusUnprocessableEntity, "capacity_exceeded", "business capacity would be exceeded"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "deadline", "operation deadline exceeded"
	default:
		return http.StatusInternalServerError, "internal", "internal server error"
	}
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_json", "message": "request body must be valid JSON", "request_id": middleware.RequestIDFrom(request.Context())}})
		return false
	}
	return true
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
