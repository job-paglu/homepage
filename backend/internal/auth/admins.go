package auth

// Everything to do with the admin_users table: the row type, the response DTO,
// and the four statements that touch it. Split out of session.go so each file
// covers one table.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Admin is an administrator row. PasswordHash never leaves this package.
type Admin struct {
	ID          string
	Email       string
	Name        string
	LastLoginAt *time.Time
	DisabledAt  *time.Time

	passwordHash string
}

// AdminDTO is the response shape from contract section 2.5. It exists as a
// separate type so a password hash cannot be serialised by accident.
type AdminDTO struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	Name        string  `json:"name"`
	LastLoginAt *string `json:"last_login_at"`
}

// DTO converts an Admin to its wire form, formatting the timestamp as RFC 3339
// in UTC like every other timestamp in the API.
func (a Admin) DTO() AdminDTO {
	dto := AdminDTO{ID: a.ID, Email: a.Email, Name: a.Name}
	if a.LastLoginAt != nil {
		formatted := a.LastLoginAt.UTC().Format(time.RFC3339)
		dto.LastLoginAt = &formatted
	}
	return dto
}

// FindAdminByEmail looks up an administrator by address, case-insensitively.
//
// A disabled account is returned rather than hidden, so the caller can still
// perform a password verification and keep the response time uniform.
func (s *Store) FindAdminByEmail(ctx context.Context, email string) (Admin, error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	const query = `SELECT id, email, name, password_hash, last_login_at, disabled_at
		FROM admin_users WHERE lower(email) = lower($1)`

	var a Admin
	err := s.pool.QueryRow(ctx, query, email).
		Scan(&a.ID, &a.Email, &a.Name, &a.passwordHash, &a.LastLoginAt, &a.DisabledAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Admin{}, ErrNoAdmin
		}
		return Admin{}, classify(fmt.Errorf("looking up administrator: %w", err))
	}
	return a, nil
}

// PasswordHash exposes the stored hash to the login handler only. It is a method
// rather than a field so no struct literal or JSON encoder can reach it.
func (a Admin) PasswordHash() string { return a.passwordHash }

// TouchLogin records a successful sign-in.
func (s *Store) TouchLogin(ctx context.Context, adminID string, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	const query = `UPDATE admin_users SET last_login_at = $2, updated_at = now() WHERE id = $1::uuid`
	if _, err := s.pool.Exec(ctx, query, adminID, at); err != nil {
		return classify(fmt.Errorf("recording last login: %w", err))
	}
	return nil
}

// UpsertAdmin creates an administrator, or updates the name and password of an
// existing one. Used only by cmd/adminctl.
func (s *Store) UpsertAdmin(ctx context.Context, email, name, passwordHash string) (created bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	// ON CONFLICT names the expression index, which is how a unique index on
	// lower(email) is targeted.
	const query = `INSERT INTO admin_users (email, name, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())
		ON CONFLICT (lower(email)) DO UPDATE
			SET name = excluded.name, password_hash = excluded.password_hash, updated_at = now()
		RETURNING (xmax = 0) AS inserted`

	if err := s.pool.QueryRow(ctx, query, email, name, passwordHash).Scan(&created); err != nil {
		return false, classify(fmt.Errorf("upserting administrator: %w", err))
	}
	return created, nil
}

// SetPassword replaces an administrator's password hash. Returns ErrNoAdmin when
// the address is unknown.
func (s *Store) SetPassword(ctx context.Context, email, passwordHash string) error {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	const query = `UPDATE admin_users SET password_hash = $2, updated_at = now()
		WHERE lower(email) = lower($1)`

	tag, err := s.pool.Exec(ctx, query, email, passwordHash)
	if err != nil {
		return classify(fmt.Errorf("setting password: %w", err))
	}
	if tag.RowsAffected() == 0 {
		return ErrNoAdmin
	}
	return nil
}
