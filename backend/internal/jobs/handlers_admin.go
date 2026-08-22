package jobs

import (
	"context"
	"net/http"

	"github.com/job-paglu/homepage/backend/internal/httpx"
	"github.com/job-paglu/homepage/backend/internal/validation"
)

// cacheNoStore is sent on every admin response. An admin list cached anywhere —
// browser, CDN, corporate proxy — is both a staleness bug and a disclosure risk.
const cacheNoStore = "no-store"

// AdminHandler serves the authenticated job endpoints.
//
// Authentication and CSRF are enforced by middleware before any method here
// runs, so these handlers contain no auth logic at all. That is deliberate: an
// access check written per-handler is an access check that gets forgotten.
type AdminHandler struct {
	service *Service
}

// NewAdminHandler wires the admin endpoints to a service.
func NewAdminHandler(service *Service) *AdminHandler {
	return &AdminHandler{service: service}
}

// List handles GET /api/admin/jobs.
func (h *AdminHandler) List(w http.ResponseWriter, r *http.Request) {
	query, err := parseListQuery(r.URL.Query(), true)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	summaries, meta, err := h.service.ListAdmin(r.Context(), query)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", cacheNoStore)
	httpx.Collection(w, r, summaries, meta)
}

// Get handles GET /api/admin/jobs/{id}.
func (h *AdminHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	job, err := h.service.GetAdmin(r.Context(), id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", cacheNoStore)
	httpx.Data(w, r, http.StatusOK, job)
}

// Create handles POST /api/admin/jobs.
func (h *AdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input JobInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	job, err := h.service.Create(r.Context(), input)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", cacheNoStore)
	w.Header().Set("Location", "/api/admin/jobs/"+job.ID)
	httpx.Data(w, r, http.StatusCreated, job)
}

// Update handles PUT /api/admin/jobs/{id}.
func (h *AdminHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var input JobInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	job, err := h.service.Update(r.Context(), id, input)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", cacheNoStore)
	httpx.Data(w, r, http.StatusOK, job)
}

// Delete handles DELETE /api/admin/jobs/{id}. The delete is soft; the row
// survives and the slug becomes reusable.
func (h *AdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", cacheNoStore)
	httpx.NoContent(w)
}

// Publish handles POST /api/admin/jobs/{id}/publish.
func (h *AdminHandler) Publish(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, h.service.Publish)
}

// Unpublish handles POST /api/admin/jobs/{id}/unpublish.
func (h *AdminHandler) Unpublish(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, h.service.Unpublish)
}

// transition is the shared body of the two state-change endpoints: identical
// except for which service method runs, and neither takes a request body.
func (h *AdminHandler) transition(
	w http.ResponseWriter,
	r *http.Request,
	change func(ctx context.Context, id string) (Job, error),
) {
	id, err := pathUUID(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	job, err := change(r.Context(), id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", cacheNoStore)
	httpx.Data(w, r, http.StatusOK, job)
}

// pathUUID reads and checks the {id} wildcard.
//
// The shape check happens here so an unparseable id is a 400 from the handler
// rather than a driver error from Postgres — and so a hostile value never
// reaches the ::uuid cast at all (contract section 6).
func pathUUID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if !validation.IsUUID(id) {
		return "", httpx.BadRequest("The job id must be a UUID.")
	}
	return id, nil
}
