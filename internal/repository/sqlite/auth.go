package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func (s *Store) CreateOrganization(ctx context.Context, id, name string, createdAt time.Time) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" {
		return domain.NewValidationError("create organization", domain.FieldViolation{Field: "organization", Rule: "id and name are required"})
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO organizations(id, name, created_at) VALUES (?, ?, ?)", id, strings.TrimSpace(name), formatTime(createdAt))
	if err != nil {
		return fmt.Errorf("insert organization: %w", err)
	}
	return nil
}

func (s *Store) CreateUser(ctx context.Context, user domain.User) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users(
			id, organization_id, email, password_hash, role, active,
			auth_generation, failed_login_count, locked_until, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID,
		user.OrganizationID,
		strings.ToLower(strings.TrimSpace(user.Email)),
		user.PasswordHash,
		string(user.Role),
		boolInt(user.Active),
		user.AuthGeneration,
		user.FailedLoginCount,
		nullableTime(user.LockedUntil),
		formatTime(user.CreatedAt),
		formatTime(user.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func scanUser(scanner interface{ Scan(...any) error }) (domain.User, error) {
	var user domain.User
	var role string
	var active int
	var locked sql.NullString
	var created, updated string
	err := scanner.Scan(
		&user.ID,
		&user.OrganizationID,
		&user.Email,
		&user.PasswordHash,
		&role,
		&active,
		&user.AuthGeneration,
		&user.FailedLoginCount,
		&locked,
		&created,
		&updated,
	)
	if err != nil {
		return domain.User{}, err
	}
	user.Role = domain.Role(role)
	user.Active = active == 1
	if locked.Valid {
		value, err := parseTime(locked.String)
		if err != nil {
			return domain.User{}, err
		}
		user.LockedUntil = &value
	}
	var parseErr error
	if user.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.User{}, parseErr
	}
	if user.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.User{}, parseErr
	}
	return user, nil
}

const selectUserColumns = `
	id, organization_id, email, password_hash, role, active,
	auth_generation, failed_login_count, locked_until, created_at, updated_at`

func (s *Store) UserByEmail(ctx context.Context, organizationID, email string) (domain.User, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+selectUserColumns+" FROM users WHERE organization_id = ? AND email = ?", organizationID, strings.ToLower(strings.TrimSpace(email)))
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, &domain.NotFoundError{Resource: "user", ID: email}
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("select user by email: %w", err)
	}
	return user, nil
}

func (s *Store) UserByID(ctx context.Context, db DBTX, organizationID, userID string) (domain.User, error) {
	row := db.QueryRowContext(ctx, "SELECT "+selectUserColumns+" FROM users WHERE organization_id = ? AND id = ?", organizationID, userID)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, &domain.NotFoundError{Resource: "user", ID: userID}
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("select user by id: %w", err)
	}
	return user, nil
}

func (s *Store) UpdateLoginFailure(ctx context.Context, userID string, now time.Time, threshold int, lockDuration time.Duration) error {
	return s.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT failed_login_count FROM users WHERE id = ?", userID).Scan(&count); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.NotFoundError{Resource: "user", ID: userID}
			}
			return fmt.Errorf("read login failure count: %w", err)
		}
		count++
		var lockedUntil any
		if count >= threshold {
			lockedUntil = formatTime(now.Add(lockDuration))
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET failed_login_count = ?, locked_until = ?, updated_at = ? WHERE id = ?`, count, lockedUntil, formatTime(now), userID)
		if err != nil {
			return fmt.Errorf("persist login failure: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read login failure update count: %w", err)
		}
		if changed != 1 {
			return &domain.NotFoundError{Resource: "user", ID: userID}
		}
		return nil
	})
}

func (s *Store) CompleteLogin(ctx context.Context, userID, upgradedHash string, session domain.Session, now time.Time) error {
	return s.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var active int
		var generation int64
		if err := tx.QueryRowContext(ctx, "SELECT active, auth_generation FROM users WHERE id = ?", userID).Scan(&active, &generation); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.NotFoundError{Resource: "user", ID: userID}
			}
			return fmt.Errorf("lock user for login: %w", err)
		}
		if active != 1 {
			return domain.ErrForbidden
		}
		if upgradedHash != "" {
			result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, failed_login_count = 0, locked_until = NULL, updated_at = ? WHERE id = ? AND auth_generation = ?`, upgradedHash, formatTime(now), userID, generation)
			if err != nil {
				return fmt.Errorf("upgrade password hash: %w", err)
			}
			if changed, _ := result.RowsAffected(); changed != 1 {
				return &domain.ConflictError{Resource: "user login", Key: userID, Cause: domain.ErrConflict}
			}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET failed_login_count = 0, locked_until = NULL, updated_at = ? WHERE id = ?`, formatTime(now), userID); err != nil {
				return fmt.Errorf("clear login failures: %w", err)
			}
		}
		session.AuthGeneration = generation
		_, err := tx.ExecContext(ctx, `INSERT INTO sessions(id, user_id, token_hash, auth_generation, expires_at, revoked_at, created_at) VALUES (?, ?, ?, ?, ?, NULL, ?)`, session.ID, userID, session.TokenHash, generation, formatTime(session.ExpiresAt), formatTime(session.CreatedAt))
		if err != nil {
			return fmt.Errorf("insert login session: %w", err)
		}
		return nil
	})
}

func (s *Store) SessionPrincipal(ctx context.Context, tokenHash string, now time.Time) (domain.Actor, error) {
	var actor domain.Actor
	var role string
	var active int
	var expires string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.organization_id, u.role, u.auth_generation, u.active, s.expires_at, s.revoked_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.auth_generation = u.auth_generation`, tokenHash).Scan(
		&actor.UserID,
		&actor.OrganizationID,
		&role,
		&actor.AuthGeneration,
		&active,
		&expires,
		&revoked,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Actor{}, domain.ErrUnauthorized
	}
	if err != nil {
		return domain.Actor{}, fmt.Errorf("select session principal: %w", err)
	}
	expiresAt, err := parseTime(expires)
	if err != nil {
		return domain.Actor{}, fmt.Errorf("parse session expiry: %w", err)
	}
	if active != 1 || revoked.Valid || !expiresAt.After(now) {
		return domain.Actor{}, domain.ErrUnauthorized
	}
	actor.Role = domain.Role(role)
	if err := actor.Validate(); err != nil {
		return domain.Actor{}, fmt.Errorf("validate persisted principal: %w", err)
	}
	return actor, nil
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`, formatTime(now), tokenHash)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked session count: %w", err)
	}
	if changed == 0 {
		return domain.ErrUnauthorized
	}
	return nil
}

func (s *Store) SetUserActive(ctx context.Context, organizationID, userID string, active bool, now time.Time) error {
	return s.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE users
			SET active = ?, auth_generation = auth_generation + 1, updated_at = ?
			WHERE organization_id = ? AND id = ?`, boolInt(active), formatTime(now), organizationID, userID)
		if err != nil {
			return fmt.Errorf("set user active state: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read user state update count: %w", err)
		}
		if changed != 1 {
			return &domain.NotFoundError{Resource: "user", ID: userID}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE user_id = ?`, formatTime(now), userID); err != nil {
			return fmt.Errorf("revoke user sessions: %w", err)
		}
		return nil
	})
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM sessions
		WHERE id IN (
			SELECT id FROM sessions
			WHERE expires_at <= ? OR revoked_at IS NOT NULL
			ORDER BY expires_at ASC
			LIMIT ?
		)`, formatTime(now), limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read deleted session count: %w", err)
	}
	return count, nil
}
