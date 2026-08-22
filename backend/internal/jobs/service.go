package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/job-paglu/homepage/backend/internal/httpx"
	"github.com/job-paglu/homepage/backend/internal/validation"
)

// Pagination bounds from contract section 3 and 4.
const (
	DefaultPerPage = 20
	MaxPerPage     = 50
	AdminPerPage   = 100
)

// Repo is the persistence surface the service needs.
//
// It exists as an interface for one concrete reason: it makes service_test.go
// possible without a database. Everything in this file is a rule, and a rule
// should not need Postgres to be proven.
type Repo interface {
	ListPublic(ctx context.Context, f PublicFilter) ([]Record, int64, error)
	GetPublicBySlug(ctx context.Context, slug string) (Record, error)
	ListAdmin(ctx context.Context, f AdminFilter) ([]Record, int64, error)
	GetByID(ctx context.Context, id string) (Record, error)
	SlugTaken(ctx context.Context, slug, excludeID string) (bool, error)
	Create(ctx context.Context, in Record) (Record, error)
	Update(ctx context.Context, id string, in Record) (Record, error)
	SoftDelete(ctx context.Context, id string) error
	Publish(ctx context.Context, id string) (Record, error)
	Unpublish(ctx context.Context, id string) (Record, error)
}

// Service holds the business rules: validation, slug derivation, and the
// status/published_at transitions. It never touches HTTP and never writes SQL.
type Service struct {
	repo        Repo
	applyPolicy validation.ApplyURLPolicy
	// now is injected so the expiry and published_at rules are testable without
	// sleeping, and so one request sees a single consistent "now".
	now func() time.Time
}

// NewService wires a service to a repository.
func NewService(repo Repo, applyPolicy validation.ApplyURLPolicy) *Service {
	return &Service{repo: repo, applyPolicy: applyPolicy, now: time.Now}
}

// ListQuery is the parsed query string for either list endpoint. The handler has
// already rejected syntactically invalid values; clamping is a rule and happens
// here.
type ListQuery struct {
	Page       int
	PerPage    int
	Q          string
	Location   string
	Country    string
	JobType    string
	RemoteType string
	Featured   *bool

	// Admin-only.
	Status         string
	IncludeDeleted bool
}

func (q ListQuery) normalise(maxPerPage int) (page, perPage int) {
	page = max(q.Page, 1)
	perPage = q.PerPage
	if perPage <= 0 {
		perPage = DefaultPerPage
	}
	perPage = min(perPage, maxPerPage)
	return page, perPage
}

func (q ListQuery) filter(page, perPage int) PublicFilter {
	return PublicFilter{
		Q:          q.Q,
		Location:   q.Location,
		Country:    q.Country,
		JobType:    q.JobType,
		RemoteType: q.RemoteType,
		Featured:   q.Featured,
		Limit:      perPage,
		Offset:     (page - 1) * perPage,
	}
}

// ------------------------------------------------------------------- reading

// ListPublic returns a page of published jobs.
func (s *Service) ListPublic(ctx context.Context, q ListQuery) ([]JobSummary, Pagination, error) {
	page, perPage := q.normalise(MaxPerPage)

	records, total, err := s.repo.ListPublic(ctx, q.filter(page, perPage))
	if err != nil {
		return nil, Pagination{}, wrapRepoError(err, "listing public jobs")
	}
	return Summaries(records), NewPagination(page, perPage, total), nil
}

// GetPublic returns a published job by slug.
func (s *Service) GetPublic(ctx context.Context, slug string) (Job, error) {
	record, err := s.repo.GetPublicBySlug(ctx, slug)
	if err != nil {
		return Job{}, wrapRepoError(err, "loading public job")
	}
	return record.Full(), nil
}

// ListAdmin returns a page across all statuses.
func (s *Service) ListAdmin(ctx context.Context, q ListQuery) ([]JobSummary, Pagination, error) {
	page, perPage := q.normalise(AdminPerPage)

	filter := AdminFilter{
		PublicFilter:   q.filter(page, perPage),
		Status:         q.Status,
		IncludeDeleted: q.IncludeDeleted,
	}
	records, total, err := s.repo.ListAdmin(ctx, filter)
	if err != nil {
		return nil, Pagination{}, wrapRepoError(err, "listing admin jobs")
	}
	return Summaries(records), NewPagination(page, perPage, total), nil
}

// GetAdmin returns any non-deleted job by id.
func (s *Service) GetAdmin(ctx context.Context, id string) (Job, error) {
	record, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Job{}, wrapRepoError(err, "loading job")
	}
	return record.Full(), nil
}

// ------------------------------------------------------------------- writing

// Create validates the input, settles the slug, and inserts.
func (s *Service) Create(ctx context.Context, in JobInput) (Job, error) {
	now := s.now().UTC()

	record, fieldErrors := s.validateInput(in, nil, now)
	if fieldErrors.Any() {
		return Job{}, httpx.ValidationFailed(fieldErrors.Map())
	}

	slug, err := s.settleSlug(ctx, in.Slug, in.Title, "")
	if err != nil {
		return Job{}, err
	}
	record.Slug = slug

	// A create that asks for "published" is publishing, so it gets a
	// publication date. Every other status leaves it null.
	if record.Status == StatusPublished {
		record.PublishedAt = &now
	}

	stored, err := s.repo.Create(ctx, record)
	if err != nil {
		return Job{}, wrapRepoError(err, "creating job")
	}
	return stored.Full(), nil
}

// Update replaces the mutable fields of an existing job.
//
// Two deliberate departures from a literal "full replacement":
//
//   - An absent slug keeps the existing one, even when the title changed. That
//     is required by contract section 4 and is what protects a live URL.
//   - An absent status keeps the existing one rather than resetting to "draft".
//     The contract defaults status to draft on create; applying the same default
//     here would mean a dashboard that saves an edit without touching the status
//     field silently unpublishes a live job. Publishing state changes through
//     the publish/unpublish endpoints, which the contract already calls the
//     preferred path.
func (s *Service) Update(ctx context.Context, id string, in JobInput) (Job, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Job{}, wrapRepoError(err, "loading job for update")
	}

	now := s.now().UTC()
	record, fieldErrors := s.validateInput(in, &existing, now)
	if fieldErrors.Any() {
		return Job{}, httpx.ValidationFailed(fieldErrors.Map())
	}

	slug, err := s.settleSlug(ctx, in.Slug, in.Title, id)
	if err != nil {
		return Job{}, err
	}
	if slug == "" {
		slug = existing.Slug
	}
	record.Slug = slug

	// published_at is set the first time a job becomes published and is never
	// rewritten afterwards, whichever route the transition took.
	record.PublishedAt = existing.PublishedAt
	if record.Status == StatusPublished && record.PublishedAt == nil {
		record.PublishedAt = &now
	}

	stored, err := s.repo.Update(ctx, id, record)
	if err != nil {
		return Job{}, wrapRepoError(err, "updating job")
	}
	return stored.Full(), nil
}

// Delete soft-deletes a job.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.repo.SoftDelete(ctx, id); err != nil {
		return wrapRepoError(err, "deleting job")
	}
	return nil
}

// Publish makes a job publicly visible.
//
// The stored record is re-validated first. The contract requires this for
// apply_url, and extending it to every field costs nothing and covers the case
// that motivated the rule: a row edited directly in the database, bypassing
// every check, must not become a live page with a `javascript:` apply link.
func (s *Service) Publish(ctx context.Context, id string) (Job, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Job{}, wrapRepoError(err, "loading job for publish")
	}

	if fieldErrors := s.validateStored(existing); fieldErrors.Any() {
		return Job{}, httpx.ValidationFailed(fieldErrors.Map())
	}

	stored, err := s.repo.Publish(ctx, id)
	if err != nil {
		return Job{}, wrapRepoError(err, "publishing job")
	}
	return stored.Full(), nil
}

// Unpublish hides a job. published_at is left intact so re-publishing keeps the
// original date.
func (s *Service) Unpublish(ctx context.Context, id string) (Job, error) {
	stored, err := s.repo.Unpublish(ctx, id)
	if err != nil {
		return Job{}, wrapRepoError(err, "unpublishing job")
	}
	return stored.Full(), nil
}

// -------------------------------------------------------------------- slugs

// settleSlug resolves the slug for a create or update.
//
// Returns "" only on update with no slug supplied, which the caller reads as
// "keep the existing one".
func (s *Service) settleSlug(ctx context.Context, supplied *string, title, excludeID string) (string, error) {
	if supplied != nil {
		normalised := NormaliseSlug(*supplied)
		// Format was already validated in validateInput; an empty result here
		// means the supplied value contained nothing usable, which was reported
		// there. Guard anyway rather than writing an empty slug.
		if normalised == "" {
			return "", httpx.ValidationFailed(map[string]string{
				"slug": "Must contain only lowercase letters, numbers and single hyphens.",
			})
		}

		taken, err := s.repo.SlugTaken(ctx, normalised, excludeID)
		if err != nil {
			return "", wrapRepoError(err, "checking slug")
		}
		if taken {
			// An explicit slug is never silently altered: the admin asked for a
			// specific URL and deserves to know it is unavailable.
			return "", httpx.SlugConflict()
		}
		return normalised, nil
	}

	if excludeID != "" {
		return "", nil // update with no slug: keep the existing one
	}

	base := DeriveSlug(title)
	slug, err := DeduplicateSlug(base, func(candidate string) (bool, error) {
		return s.repo.SlugTaken(ctx, candidate, excludeID)
	})
	if err != nil {
		return "", wrapRepoError(err, "de-duplicating slug")
	}
	return slug, nil
}

// -------------------------------------------------------------- error mapping

// wrapRepoError converts a repository sentinel into the client-facing error.
//
// This is the only place the two vocabularies meet, so a new sentinel has one
// place to be handled and an unrecognised error can only ever become a 500.
func wrapRepoError(err error, operation string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return httpx.JobNotFound()
	case errors.Is(err, ErrSlugConflict):
		return httpx.SlugConflict()
	case errors.Is(err, ErrUnavailable):
		return httpx.ServiceUnavailable(fmt.Errorf("%s: %w", operation, err))
	default:
		return httpx.Internal(fmt.Errorf("%s: %w", operation, err))
	}
}
