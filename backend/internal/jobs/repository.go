package jobs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors the service layer branches on. They exist so the service never
// has to know what a pgx error looks like.
var (
	// ErrNotFound means no row matched — including a row that exists but is
	// soft-deleted, or not publicly visible.
	ErrNotFound = errors.New("job not found")
	// ErrSlugConflict is the unique-index violation on jobs_slug_active_key.
	ErrSlugConflict = errors.New("slug already in use")
	// ErrUnavailable means Postgres could not be reached or did not answer in
	// time. It becomes a 503 rather than a 500: a Neon instance resuming from
	// scale-to-zero is a transient condition, and the frontend renders it as
	// retryable.
	ErrUnavailable = errors.New("database unavailable")
)

// queryTimeout caps every statement.
//
// Without it a Neon cold start — or a network partition — leaves goroutines
// parked on a socket while the platform keeps accepting requests, and the
// instance dies of exhaustion instead of returning an honest 503. Three seconds is longer
// than any query here needs and shorter than a visitor will wait.
const queryTimeout = 3 * time.Second

// publicVisible is the public visibility predicate, defined exactly once.
//
// Every public query embeds this constant, so there is no code path where a
// handler could forget it and leak a draft. That is the whole reason it is a
// constant and not a helper that returns a WHERE clause: a helper can be left
// uncalled.
const publicVisible = `status = 'published' AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > now())`

// jobColumns is the SELECT list, in the order scanRecord reads them.
// view_count and apply_click_count exist in the table for later analytics and
// are deliberately not selected: nothing in the API returns them.
const jobColumns = `id, slug, title, description, salary, qualification, job_type, location,
	apply_url, company, country, remote_type, experience_level, category,
	is_featured, status, published_at, expires_at, created_at, updated_at, deleted_at`

// Repository is the only place SQL lives.
//
// Every statement uses $n placeholders. The dynamic filters on the list
// endpoints allocate their placeholders programmatically (see conditions), so no
// value is ever formatted into a query string.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ------------------------------------------------------------------- filters

// PublicFilter is the parsed, validated query string of GET /api/jobs.
// Empty strings mean "no filter"; the handler has already rejected malformed
// values, so the repository does not re-validate.
type PublicFilter struct {
	Q          string
	Location   string
	Country    string
	JobType    string
	RemoteType string
	Featured   *bool
	Limit      int
	Offset     int
}

// AdminFilter is PublicFilter plus the two admin-only switches.
type AdminFilter struct {
	PublicFilter
	Status         string
	IncludeDeleted bool
}

// conditions accumulates WHERE fragments together with their arguments,
// allocating $n indices as it goes.
//
// This is the mechanism that keeps the dynamic filters injection-proof: a caller
// can only obtain a placeholder by handing over the value, and only the returned
// placeholder text is ever concatenated into SQL.
type conditions struct {
	parts []string
	args  []any
}

// placeholder stores v and returns the "$n" that refers to it.
func (c *conditions) placeholder(v any) string {
	c.args = append(c.args, v)
	return "$" + strconv.Itoa(len(c.args))
}

// add appends a fragment. The fragment must contain only SQL and placeholders
// obtained from placeholder — never a caller-supplied value.
func (c *conditions) add(fragment string) {
	c.parts = append(c.parts, fragment)
}

func (c *conditions) where() string {
	if len(c.parts) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(c.parts, " AND ")
}

// likeEscaper neutralises the LIKE metacharacters so a search for "50%" is a
// search for "50%" and not for "50<anything>".
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func containsPattern(value string) string { return "%" + likeEscaper.Replace(value) + "%" }

// applyCommonFilters adds the filters shared by the public and admin lists.
func applyCommonFilters(c *conditions, f PublicFilter) {
	if f.Q != "" {
		p := c.placeholder(containsPattern(f.Q))
		// One placeholder reused across four columns: the same pattern, so
		// there is no reason to send it four times.
		c.add("(title ILIKE " + p + " OR coalesce(company, '') ILIKE " + p +
			" OR location ILIKE " + p + " OR description ILIKE " + p + ")")
	}
	if f.Location != "" {
		c.add("location ILIKE " + c.placeholder(containsPattern(f.Location)))
	}
	if f.Country != "" {
		// Exact but case-insensitive, per contract section 3.
		c.add("lower(coalesce(country, '')) = lower(" + c.placeholder(f.Country) + ")")
	}
	if f.JobType != "" {
		c.add("job_type = " + c.placeholder(f.JobType))
	}
	if f.RemoteType != "" {
		c.add("remote_type = " + c.placeholder(f.RemoteType))
	}
	if f.Featured != nil {
		c.add("is_featured = " + c.placeholder(*f.Featured))
	}
}

// ------------------------------------------------------------- public queries

// ListPublic returns one page of publicly visible jobs and the total number of
// matching rows.
//
// The count comes from a window function rather than a second query: window
// functions are evaluated before LIMIT, so `count(*) OVER ()` is the full match
// count, and one round trip matters when the database may be resuming from
// scale-to-zero.
func (r *Repository) ListPublic(ctx context.Context, f PublicFilter) ([]Record, int64, error) {
	c := &conditions{}
	c.add(publicVisible)
	applyCommonFilters(c, f)

	limit := c.placeholder(f.Limit)
	offset := c.placeholder(f.Offset)

	query := `SELECT ` + jobColumns + `, count(*) OVER () AS total
		FROM jobs` + c.where() + `
		ORDER BY is_featured DESC, published_at DESC NULLS LAST, created_at DESC
		LIMIT ` + limit + ` OFFSET ` + offset

	return r.queryPage(ctx, query, c.args)
}

// GetPublicBySlug returns a job by slug, or ErrNotFound when it is not publicly
// visible. The predicate is in the query, so a draft cannot be returned even by
// a caller that forgets to check.
func (r *Repository) GetPublicBySlug(ctx context.Context, slug string) (Record, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE slug = $1 AND ` + publicVisible
	return r.queryOne(ctx, query, slug)
}

// -------------------------------------------------------------- admin queries

// ListAdmin returns one page across all statuses, ignoring expiry.
func (r *Repository) ListAdmin(ctx context.Context, f AdminFilter) ([]Record, int64, error) {
	c := &conditions{}
	if !f.IncludeDeleted {
		c.add("deleted_at IS NULL")
	}
	if f.Status != "" {
		c.add("status = " + c.placeholder(f.Status))
	}
	applyCommonFilters(c, f.PublicFilter)

	limit := c.placeholder(f.Limit)
	offset := c.placeholder(f.Offset)

	query := `SELECT ` + jobColumns + `, count(*) OVER () AS total
		FROM jobs` + c.where() + `
		ORDER BY updated_at DESC
		LIMIT ` + limit + ` OFFSET ` + offset

	return r.queryPage(ctx, query, c.args)
}

// GetByID returns any non-deleted job by UUID. The id has already been checked
// for shape by the handler, so the ::uuid cast cannot fail on user input.
func (r *Repository) GetByID(ctx context.Context, id string) (Record, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE id = $1::uuid AND deleted_at IS NULL`
	return r.queryOne(ctx, query, id)
}

// SlugTaken reports whether slug is in use by a non-deleted job other than
// excludeID. Pass an empty excludeID on create.
func (r *Repository) SlugTaken(ctx context.Context, slug, excludeID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// A NULL third argument disables the exclusion, which keeps this one
	// statement instead of two nearly identical ones.
	var exclude *string
	if excludeID != "" {
		exclude = &excludeID
	}

	const query = `SELECT EXISTS (
		SELECT 1 FROM jobs
		WHERE slug = $1 AND deleted_at IS NULL
		  AND ($2::uuid IS NULL OR id <> $2::uuid)
	)`

	var taken bool
	if err := r.pool.QueryRow(ctx, query, slug, exclude).Scan(&taken); err != nil {
		return false, classify(fmt.Errorf("checking slug availability: %w", err))
	}
	return taken, nil
}

// Create inserts a job and returns the stored row.
//
// updated_at is written explicitly here and in every UPDATE. There is no
// database trigger: a trigger would be invisible at the call site, and one
// consistent convention beats two mechanisms.
func (r *Repository) Create(ctx context.Context, in Record) (Record, error) {
	const query = `INSERT INTO jobs (
			slug, title, description, salary, qualification, job_type, location,
			apply_url, company, country, remote_type, experience_level, category,
			is_featured, status, published_at, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, now(), now()
		)
		RETURNING ` + jobColumns

	return r.queryOne(ctx, query,
		in.Slug, in.Title, in.Description, in.Salary, in.Qualification, string(in.JobType), in.Location,
		in.ApplyURL, in.Company, in.Country, remotePtr(in.RemoteType), in.ExperienceLevel, in.Category,
		in.IsFeatured, string(in.Status), in.PublishedAt, in.ExpiresAt,
	)
}

// Update replaces the mutable fields of a non-deleted job.
func (r *Repository) Update(ctx context.Context, id string, in Record) (Record, error) {
	const query = `UPDATE jobs SET
			slug = $2, title = $3, description = $4, salary = $5, qualification = $6,
			job_type = $7, location = $8, apply_url = $9, company = $10, country = $11,
			remote_type = $12, experience_level = $13, category = $14, is_featured = $15,
			status = $16, published_at = $17, expires_at = $18, updated_at = now()
		WHERE id = $1::uuid AND deleted_at IS NULL
		RETURNING ` + jobColumns

	return r.queryOne(ctx, query, id,
		in.Slug, in.Title, in.Description, in.Salary, in.Qualification,
		string(in.JobType), in.Location, in.ApplyURL, in.Company, in.Country,
		remotePtr(in.RemoteType), in.ExperienceLevel, in.Category, in.IsFeatured,
		string(in.Status), in.PublishedAt, in.ExpiresAt,
	)
}

// SoftDelete sets deleted_at. The row survives so it can be recovered, and
// jobs_slug_active_key is partial on deleted_at IS NULL so the slug becomes
// reusable immediately (contract section 4).
func (r *Repository) SoftDelete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	const query = `UPDATE jobs SET deleted_at = now(), updated_at = now()
		WHERE id = $1::uuid AND deleted_at IS NULL`

	tag, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return classify(fmt.Errorf("soft-deleting job: %w", err))
	}
	if tag.RowsAffected() == 0 {
		// Already deleted or never existed — indistinguishable, and the contract
		// says both are 404.
		return ErrNotFound
	}
	return nil
}

// Publish sets status and stamps published_at only if it was null, so
// re-publishing does not rewrite the original publication date.
func (r *Repository) Publish(ctx context.Context, id string) (Record, error) {
	const query = `UPDATE jobs SET
			status = 'published',
			published_at = coalesce(published_at, now()),
			updated_at = now()
		WHERE id = $1::uuid AND deleted_at IS NULL
		RETURNING ` + jobColumns

	return r.queryOne(ctx, query, id)
}

// Unpublish hides a job from the public API and leaves published_at intact.
func (r *Repository) Unpublish(ctx context.Context, id string) (Record, error) {
	const query = `UPDATE jobs SET status = 'unpublished', updated_at = now()
		WHERE id = $1::uuid AND deleted_at IS NULL
		RETURNING ` + jobColumns

	return r.queryOne(ctx, query, id)
}

// ---------------------------------------------------------------- execution

func (r *Repository) queryOne(ctx context.Context, query string, args ...any) (Record, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return Record{}, classify(err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Record{}, classify(err)
		}
		return Record{}, ErrNotFound
	}

	record, err := scanRecord(rows)
	if err != nil {
		return Record{}, classify(err)
	}
	return record, rows.Err()
}

func (r *Repository) queryPage(ctx context.Context, query string, args []any) ([]Record, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, classify(err)
	}
	defer rows.Close()

	records := make([]Record, 0, 20)
	var total int64
	for rows.Next() {
		record, rowTotal, err := scanRecordWithTotal(rows)
		if err != nil {
			return nil, 0, classify(err)
		}
		records = append(records, record)
		total = rowTotal
	}
	if err := rows.Err(); err != nil {
		return nil, 0, classify(err)
	}
	// No rows means no window-function value to read, and zero is correct.
	return records, total, nil
}
