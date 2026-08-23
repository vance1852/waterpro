package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	actorKey     contextKey = "actor"
)

type Authenticator interface {
	Authenticate(context.Context, string) (domain.Actor, error)
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if len(requestID) < 8 || len(requestID) > 128 {
			requestID = randomID()
		}
		response.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDKey, requestID)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func Recovery(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(request.Context(), "http panic recovered", "request_id", RequestIDFrom(request.Context()), "panic", recovered, "stack", string(debug.Stack()))
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(http.StatusInternalServerError)
				_, _ = response.Write([]byte(`{"error":{"code":"internal","message":"internal server error","request_id":"` + RequestIDFrom(request.Context()) + `"}}`))
			}
		}()
		next.ServeHTTP(response, request)
	})
}

func AccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		logger.InfoContext(request.Context(), "http request",
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.status,
			"bytes", recorder.bytes,
			"duration_ms", time.Since(started).Milliseconds(),
			"request_id", RequestIDFrom(request.Context()),
		)
	})
}

func RequireAuth(auth Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		header := strings.TrimSpace(request.Header.Get("Authorization"))
		parts := strings.Fields(header)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeUnauthorized(response, RequestIDFrom(request.Context()))
			return
		}
		actor, err := auth.Authenticate(request.Context(), parts[1])
		if err != nil {
			writeUnauthorized(response, RequestIDFrom(request.Context()))
			return
		}
		ctx := context.WithValue(request.Context(), actorKey, actor)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func RequestIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func ActorFrom(ctx context.Context) (domain.Actor, bool) {
	actor, ok := ctx.Value(actorKey).(domain.Actor)
	return actor, ok
}

func randomID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "request-unknown"
	}
	return hex.EncodeToString(buffer)
}

func writeUnauthorized(response http.ResponseWriter, requestID string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusUnauthorized)
	_, _ = response.Write([]byte(`{"error":{"code":"unauthorized","message":"authentication required","request_id":"` + requestID + `"}}`))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	written, err := r.ResponseWriter.Write(body)
	r.bytes += written
	return written, err
}
