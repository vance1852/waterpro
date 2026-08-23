package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vance1852/waterpro/internal/auth"
	"github.com/vance1852/waterpro/internal/domain"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
)

type BootstrapConfig struct {
	OrganizationID     string
	OrganizationName   string
	SupervisorEmail    string
	SupervisorPassword string
}

func Bootstrap(ctx context.Context, store *repository.Store, config BootstrapConfig) error {
	if config.OrganizationID == "" {
		config.OrganizationID = "waterpro-default"
	}
	if config.OrganizationName == "" {
		config.OrganizationName = "Water Source Protection Authority"
	}
	if config.SupervisorEmail == "" || config.SupervisorPassword == "" {
		return nil
	}
	now := time.Now().UTC()
	if err := store.CreateOrganization(ctx, config.OrganizationID, config.OrganizationName, now); err != nil && !isUniqueConstraint(err) {
		return fmt.Errorf("bootstrap organization: %w", err)
	}
	if _, err := store.UserByEmail(ctx, config.OrganizationID, config.SupervisorEmail); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("check bootstrap supervisor: %w", err)
	}
	hash, err := auth.HashPassword(config.SupervisorPassword)
	if err != nil {
		return fmt.Errorf("hash bootstrap supervisor password: %w", err)
	}
	user := domain.User{
		ID: uuid.NewString(), OrganizationID: config.OrganizationID, Email: config.SupervisorEmail,
		PasswordHash: hash, Role: domain.RoleProtectionSupervisor, Active: true,
		AuthGeneration: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateUser(ctx, user); err != nil {
		return fmt.Errorf("create bootstrap supervisor: %w", err)
	}
	return nil
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "constraint failed")
}
