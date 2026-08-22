package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Session lifetimes from contract section 5.
//
// Two clocks, both enforced server-side. Idle expiry logs out a forgotten tab;
// absolute expiry bounds the damage from a stolen cookie no matter how actively
// it is used. The cookie's Max-Age is a convenience for the browser and is never
// the authority — a client that keeps sending an old cookie gets a 401 from the
// database, not from the browser's good manners.
const (
	IdleExpiry     = 12 * time.Hour
	AbsoluteExpiry = 7 * 24 * time.Hour
)

// sessionTokenBytes is 32, as required by the contract. Base64url-encoded
// without padding that is 43 characters.
const sessionTokenBytes = 32

// Store errors.
var (
	// ErrNoSession means there is no live session for the presented token —
	// unknown, expired, idle-timed-out, or belonging to a disabled account. The
	// caller must not distinguish these to a client.
	ErrNoSession = errors.New("no live session")
	// ErrNoAdmin means no administrator matches.
	ErrNoAdmin = errors.New("no such administrator")
	// ErrUnavailable means Postgres could not be reached.
	ErrUnavailable = errors.New("database unavailable")
)

// storeTimeout caps every statement in this package, for the same reason
// jobs.queryTimeout exists: a hung database must not accumulate goroutines.
const storeTimeout = 3 * time.Second

// Session is a live server-side session. The raw token is deliberately not a
// field: it exists only in the cookie and in the response that created it.
type Session struct {
	ID         string
	AdminID    string
	CSRFToken  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// Store is the SQL for admin_users and sessions. Same rule as the jobs
// repository: every statement is parameterised and nothing above this layer
// writes SQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ------------------------------------------------------------------- tokens

// newToken returns a fresh session token and the SHA-256 of it.
//
// Only the digest is stored. A dump of the sessions table therefore yields no
// usable cookie, and there is nothing to reverse: the token has 256 bits of
// entropy, so the hash needs no salt or stretching — unlike a password, it
// cannot be guessed from a dictionary.
func newToken() (token string, digest []byte, err error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generating session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:], nil
}

// hashToken digests a token presented by a client, for lookup.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// newCSRFToken returns a 32-byte random token, stored in plaintext because the
// client has to be able to echo it back for comparison.
func newCSRFToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating csrf token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ----------------------------------------------------------------- sessions

// CreateSession issues a new session and returns the raw token, which is the
// only moment it exists outside a cookie.
//
// expires_at holds the absolute deadline; the idle deadline is derived from
// last_seen_at at lookup time. Storing only one of the two would mean either no
// idle timeout or a row that has to be rewritten on every request.
func (s *Store) CreateSession(ctx context.Context, adminID, userAgent, ip string) (string, Session, error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	token, digest, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := newCSRFToken()
	if err != nil {
		return "", Session{}, err
	}

	const query = `INSERT INTO sessions (admin_id, token_hash, csrf_token, created_at, last_seen_at, expires_at, user_agent, ip)
		VALUES ($1::uuid, $2, $3, now(), now(), now() + make_interval(secs => $4), $5, $6)
		RETURNING id, admin_id, csrf_token, created_at, last_seen_at, expires_at`

	// The user agent is truncated rather than rejected: it is diagnostic only,
	// and a 10 KB header should not fail a login.
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}

	var out Session
	err = s.pool.QueryRow(ctx, query, adminID, digest, csrf, AbsoluteExpiry.Seconds(), userAgent, ip).
		Scan(&out.ID, &out.AdminID, &out.CSRFToken, &out.CreatedAt, &out.LastSeenAt, &out.ExpiresAt)
	if err != nil {
		return "", Session{}, classify(fmt.Errorf("creating session: %w", err))
	}
	return token, out, nil
}

// LookupSession resolves a raw token to its session and administrator.
//
// Both expiry rules and the disabled-account check are in the SQL, so there is
// no code path that can authenticate a stale session by forgetting a check.
func (s *Store) LookupSession(ctx context.Context, token string) (Session, Admin, error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	const query = `SELECT
			s.id, s.admin_id, s.csrf_token, s.created_at, s.last_seen_at, s.expires_at,
			a.id, a.email, a.name, a.password_hash, a.last_login_at, a.disabled_at
		FROM sessions s
		JOIN admin_users a ON a.id = s.admin_id
		WHERE s.token_hash = $1
		  AND s.expires_at > now()
		  AND s.last_seen_at > now() - make_interval(secs => $2)
		  AND a.disabled_at IS NULL`

	var (
		session Session
		admin   Admin
	)
	err := s.pool.QueryRow(ctx, query, hashToken(token), IdleExpiry.Seconds()).Scan(
		&session.ID, &session.AdminID, &session.CSRFToken,
		&session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt,
		&admin.ID, &admin.Email, &admin.Name, &admin.passwordHash,
		&admin.LastLoginAt, &admin.DisabledAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, Admin{}, ErrNoSession
		}
		return Session{}, Admin{}, classify(fmt.Errorf("looking up session: %w", err))
	}
	return session, admin, nil
}

// TouchSession slides the idle window forward.
//
// Guarded by an interval so a busy dashboard does not write a row per request:
// one UPDATE per minute is enough to keep an active session alive and turns a
// per-request write into a rounding error.
func (s *Store) TouchSession(ctx context.Context, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	const query = `UPDATE sessions SET last_seen_at = now()
		WHERE id = $1::uuid AND last_seen_at < now() - interval '1 minute'`

	if _, err := s.pool.Exec(ctx, query, sessionID); err != nil {
		return classify(fmt.Errorf("touching session: %w", err))
	}
	return nil
}

// DeleteSession removes the session for a raw token. Deleting an unknown token
// is not an error — logout is idempotent.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token)); err != nil {
		return classify(fmt.Errorf("deleting session: %w", err))
	}
	return nil
}

// DeleteExpiredSessions removes rows past either deadline.
//
// Sessions cascade on admin deletion, so this only collects natural expiry. It
// is housekeeping, not a security control: LookupSession already refuses an
// expired row, so a missed sweep costs disk, not safety.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	const query = `DELETE FROM sessions
		WHERE expires_at <= now() OR last_seen_at <= now() - make_interval(secs => $1)`

	tag, err := s.pool.Exec(ctx, query, IdleExpiry.Seconds())
	if err != nil {
		return 0, classify(fmt.Errorf("deleting expired sessions: %w", err))
	}
	return tag.RowsAffected(), nil
}

// classify maps a driver failure to ErrUnavailable where appropriate, for the
// same reason the jobs repository does: an unreachable database is a retryable
// 503, not a 500.
func classify(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"), // connection exception
			pgErr.Code == "57P01", // admin shutdown
			pgErr.Code == "57P02", // crash shutdown
			pgErr.Code == "57P03", // cannot connect now (Neon resuming)
			pgErr.Code == "53300": // too many connections
			return fmt.Errorf("%w: %s", ErrUnavailable, pgErr.Code)
		}
		return err
	}

	var connectErr *pgconn.ConnectError
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.As(err, &connectErr),
		errors.As(err, &netErr):
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}
