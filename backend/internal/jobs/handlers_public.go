package jobs

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/job-paglu/homepage/backend/internal/httpx"
	"github.com/job-paglu/homepage/backend/internal/validation"
)

// Cache directives from contract section 3.
//
// max-age=0 keeps the browser honest while s-maxage lets the CDN absorb the
// traffic, and stale-while-revalidate means a Neon cold start is invisible to a
// visitor: the edge serves the previous copy while the refresh happens behind it.
const (
	cacheJobList   = "public, max-age=0, s-maxage=60, stale-while-revalidate=300"
	cacheJobDetail = "public, max-age=0, s-maxage=300, stale-while-revalidate=600"
)

// maxSearchLen caps the `q` parameter (contract section 3).
const maxSearchLen = 100

// PublicHandler serves the two unauthenticated job endpoints.
//
// It holds a service, never a pool: a handler that could reach the database
// directly could bypass the visibility predicate, which is the one thing this
// layering is there to prevent.
type PublicHandler struct {
	service *Service
}

// NewPublicHandler wires the public endpoints to a service.
func NewPublicHandler(service *Service) *PublicHandler {
	return &PublicHandler{service: service}
}

// List handles GET /api/jobs.
func (h *PublicHandler) List(w http.ResponseWriter, r *http.Request) {
	query, err := parseListQuery(r.URL.Query(), false)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	summaries, meta, err := h.service.ListPublic(r.Context(), query)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", cacheJobList)
	httpx.Collection(w, r, summaries, meta)
}

// Detail handles GET /api/jobs/{slug}.
func (h *PublicHandler) Detail(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		httpx.Fail(w, r, httpx.JobNotFound())
		return
	}

	job, err := h.service.GetPublic(r.Context(), slug)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", cacheJobDetail)
	httpx.Data(w, r, http.StatusOK, job)
}

// ------------------------------------------------------------ query parsing

// parseListQuery reads the query string for either list endpoint.
//
// A malformed parameter is a 400 rather than a silently ignored one: a frontend
// sending `?page=abc` has a bug, and quietly serving page 1 hides it. Values that
// are merely out of range are clamped instead, because `?page=0` from a hand-typed
// URL is a reasonable thing to be forgiving about (contract section 3).
func parseListQuery(values url.Values, admin bool) (ListQuery, error) {
	var q ListQuery

	page, err := parseInt(values, "page", 1)
	if err != nil {
		return q, err
	}
	q.Page = page

	perPage, err := parseInt(values, "per_page", DefaultPerPage)
	if err != nil {
		return q, err
	}
	q.PerPage = perPage

	q.Q = validation.CleanSingleLine(values.Get("q"))
	if validation.Length(q.Q) > maxSearchLen {
		return q, httpx.BadRequest("The q parameter must be at most 100 characters.")
	}

	q.Location = validation.CleanSingleLine(values.Get("location"))
	q.Country = validation.CleanSingleLine(values.Get("country"))

	if q.JobType, err = parseEnum(values, "job_type", TypeValues); err != nil {
		return q, err
	}
	if q.RemoteType, err = parseEnum(values, "remote_type", RemoteValues); err != nil {
		return q, err
	}
	if q.Featured, err = parseOptionalBool(values, "featured"); err != nil {
		return q, err
	}

	if admin {
		if q.Status, err = parseEnum(values, "status", StatusValues); err != nil {
			return q, err
		}
		includeDeleted, err := parseOptionalBool(values, "include_deleted")
		if err != nil {
			return q, err
		}
		q.IncludeDeleted = includeDeleted != nil && *includeDeleted
	}

	return q, nil
}

func parseInt(values url.Values, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, httpx.BadRequest("The " + name + " parameter must be a number.")
	}
	return parsed, nil
}

func parseEnum(values url.Values, name string, allowed []string) (string, error) {
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		return "", nil
	}
	for _, candidate := range allowed {
		if raw == candidate {
			return raw, nil
		}
	}
	return "", httpx.BadRequest("The " + name + " parameter must be one of: " + strings.Join(allowed, ", ") + ".")
}

// parseOptionalBool distinguishes absent from false, which `featured` needs:
// absent means "no filter", false means "only non-featured jobs".
func parseOptionalBool(values url.Values, name string) (*bool, error) {
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		return nil, nil
	}
	switch raw {
	case "true":
		value := true
		return &value, nil
	case "false":
		value := false
		return &value, nil
	default:
		return nil, httpx.BadRequest("The " + name + " parameter must be true or false.")
	}
}
