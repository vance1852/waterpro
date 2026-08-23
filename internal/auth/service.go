package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vance1852/waterpro/internal/domain"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
	"golang.org/x/crypto/bcrypt"
)

const legacyPrefix = "sha256$"

type Service struct {
	store          *repository.Store
	sessionTTL     time.Duration
	clock          func() time.Time
	loginThreshold int
	lockDuration   time.Duration
}

type LoginCommand struct {
	OrganizationID string `json:"organization_id"`
	Email          string `json:"email"`
	Password       string `json:"password"`
}

type LoginResult struct {
	Token     string       `json:"token"`
	ExpiresAt time.Time    `json:"expires_at"`
	Actor     domain.Actor `json:"actor"`
}

func NewService(store *repository.Store, sessionTTL time.Duration) *Service {
	return &Service{
		store:          store,
		sessionTTL:     sessionTTL,
		clock:          time.Now,
		loginThreshold: 5,
		lockDuration:   15 * time.Minute,
	}
}

func (s *Service) Login(ctx context.Context, command LoginCommand) (LoginResult, error) {
	command.Email = strings.ToLower(strings.TrimSpace(command.Email))
	var violations []domain.FieldViolation
	if command.OrganizationID == "" {
		violations = append(violations, domain.FieldViolation{Field: "organization_id", Rule: "is required"})
	}
	if command.Email == "" || !strings.Contains(command.Email, "@") {
		violations = append(violations, domain.FieldViolation{Field: "email", Rule: "must be a valid address"})
	}
	if len(command.Password) < 10 {
		violations = append(violations, domain.FieldViolation{Field: "password", Rule: "must contain at least 10 characters"})
	}
	if len(violations) > 0 {
		return LoginResult{}, domain.NewValidationError("login", violations...)
	}
	user, err := s.store.UserByEmail(ctx, command.OrganizationID, command.Email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return LoginResult{}, domain.ErrUnauthorized
		}
		return LoginResult{}, fmt.Errorf("load login user: %w", err)
	}
	now := s.clock().UTC()
	if !user.Active {
		return LoginResult{}, domain.ErrForbidden
	}
	if user.LockedUntil != nil && user.LockedUntil.After(now) {
		return LoginResult{}, fmt.Errorf("account locked until %s: %w", user.LockedUntil.Format(time.RFC3339), domain.ErrForbidden)
	}
	valid, upgrade, err := verifyPassword(user.PasswordHash, command.Password)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verify password: %w", err)
	}
	if !valid {
		if err := s.store.UpdateLoginFailure(ctx, user.ID, now, s.loginThreshold, s.lockDuration); err != nil {
			return LoginResult{}, fmt.Errorf("record login failure: %w", err)
		}
		return LoginResult{}, domain.ErrUnauthorized
	}
	var upgradedHash string
	if upgrade {
		encoded, err := bcrypt.GenerateFromPassword([]byte(command.Password), bcrypt.DefaultCost)
		if err != nil {
			return LoginResult{}, fmt.Errorf("upgrade password: %w", err)
		}
		upgradedHash = string(encoded)
	}
	token, tokenHash, err := generateToken()
	if err != nil {
		return LoginResult{}, err
	}
	session := domain.Session{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: now.Add(s.sessionTTL),
		CreatedAt: now,
	}
	if err := s.store.CompleteLogin(ctx, user.ID, upgradedHash, session, now); err != nil {
		return LoginResult{}, fmt.Errorf("complete login: %w", err)
	}
	actor := domain.Actor{UserID: user.ID, OrganizationID: user.OrganizationID, Role: user.Role, AuthGeneration: user.AuthGeneration}
	return LoginResult{Token: token, ExpiresAt: session.ExpiresAt, Actor: actor}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (domain.Actor, error) {
	if strings.TrimSpace(token) == "" {
		return domain.Actor{}, domain.ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(token))
	actor, err := s.store.SessionPrincipal(ctx, hex.EncodeToString(hash[:]), s.clock().UTC())
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			return domain.Actor{}, domain.ErrUnauthorized
		}
		return domain.Actor{}, fmt.Errorf("authenticate session: %w", err)
	}
	return actor, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return domain.ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(token))
	if err := s.store.RevokeSession(ctx, hex.EncodeToString(hash[:]), s.clock().UTC()); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

func (s *Service) SetUserActive(ctx context.Context, actor domain.Actor, userID string, active bool) error {
	if !actor.CanSupervise() {
		return domain.ErrForbidden
	}
	if actor.UserID == userID && !active {
		return domain.NewValidationError("deactivate user", domain.FieldViolation{Field: "user_id", Rule: "supervisor cannot deactivate their current account"})
	}
	if err := s.store.SetUserActive(ctx, actor.OrganizationID, userID, active, s.clock().UTC()); err != nil {
		return fmt.Errorf("set user active state: %w", err)
	}
	return nil
}

func generateToken() (string, string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(buffer)
	hash := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(hash[:]), nil
}

func verifyPassword(stored, supplied string) (valid bool, upgrade bool, err error) {
	if strings.HasPrefix(stored, legacyPrefix) {
		expected, decodeErr := hex.DecodeString(strings.TrimPrefix(stored, legacyPrefix))
		if decodeErr != nil || len(expected) != sha256.Size {
			return false, false, fmt.Errorf("invalid legacy password encoding")
		}
		actual := sha256.Sum256([]byte(supplied))
		return subtle.ConstantTimeCompare(expected, actual[:]) == 1, true, nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(supplied)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, false, nil
}

func HashPassword(password string) (string, error) {
	if len(password) < 10 {
		return "", domain.NewValidationError("hash password", domain.FieldViolation{Field: "password", Rule: "must contain at least 10 characters"})
	}
	value, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(value), nil
}
