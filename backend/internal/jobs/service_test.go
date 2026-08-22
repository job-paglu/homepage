package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/job-paglu/homepage/backend/internal/httpx"
	"github.com/job-paglu/homepage/backend/internal/validation"
)

// ---------------------------------------------------------------- fake repo

// fakeRepo is an in-memory Repo. It exists so every rule in service.go can be
// proven without Postgres, which is the reason Repo is an interface at all.
type fakeRepo struct {
	records map[string]*Record
	order   []string
	nextID  int

	// failWith, when set, is returned by every method. Used to prove the
	// repository-error mapping without a real outage.
	failWith error

	createdWith []Record
	slugChecks  []string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{records: map[string]*Record{}}
}

func (f *fakeRepo) seed(r Record) *Record {
	if r.ID == "" {
		f.nextID++
		r.ID = uuidFor(f.nextID)
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	stored := r
	f.records[r.ID] = &stored
	f.order = append(f.order, r.ID)
	return &stored
}

func uuidFor(n int) string {
	return "00000000-0000-4000-8000-" + strings.Repeat("0", 12-len(itoa(n))) + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func (f *fakeRepo) ListPublic(_ context.Context, _ PublicFilter) ([]Record, int64, error) {
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	var out []Record
	for _, id := range f.order {
		r := f.records[id]
		if r.Status == StatusPublished && r.DeletedAt == nil {
			out = append(out, *r)
		}
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) GetPublicBySlug(_ context.Context, slug string) (Record, error) {
	if f.failWith != nil {
		return Record{}, f.failWith
	}
	for _, id := range f.order {
		r := f.records[id]
		if r.Slug == slug && r.Status == StatusPublished && r.DeletedAt == nil {
			return *r, nil
		}
	}
	return Record{}, ErrNotFound
}

func (f *fakeRepo) ListAdmin(_ context.Context, filter AdminFilter) ([]Record, int64, error) {
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	var out []Record
	for _, id := range f.order {
		r := f.records[id]
		if r.DeletedAt != nil && !filter.IncludeDeleted {
			continue
		}
		out = append(out, *r)
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) GetByID(_ context.Context, id string) (Record, error) {
	if f.failWith != nil {
		return Record{}, f.failWith
	}
	r, ok := f.records[id]
	if !ok || r.DeletedAt != nil {
		return Record{}, ErrNotFound
	}
	return *r, nil
}

func (f *fakeRepo) SlugTaken(_ context.Context, slug, excludeID string) (bool, error) {
	if f.failWith != nil {
		return false, f.failWith
	}
	f.slugChecks = append(f.slugChecks, slug)
	for id, r := range f.records {
		if r.DeletedAt == nil && r.Slug == slug && id != excludeID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) Create(_ context.Context, in Record) (Record, error) {
	if f.failWith != nil {
		return Record{}, f.failWith
	}
	f.createdWith = append(f.createdWith, in)
	f.nextID++
	in.ID = uuidFor(f.nextID)
	in.CreatedAt = time.Now().UTC()
	in.UpdatedAt = in.CreatedAt
	stored := in
	f.records[in.ID] = &stored
	f.order = append(f.order, in.ID)
	return stored, nil
}

func (f *fakeRepo) Update(_ context.Context, id string, in Record) (Record, error) {
	if f.failWith != nil {
		return Record{}, f.failWith
	}
	existing, ok := f.records[id]
	if !ok || existing.DeletedAt != nil {
		return Record{}, ErrNotFound
	}
	in.ID = id
	in.CreatedAt = existing.CreatedAt
	in.UpdatedAt = time.Now().UTC()
	*existing = in
	return in, nil
}

func (f *fakeRepo) SoftDelete(_ context.Context, id string) error {
	if f.failWith != nil {
		return f.failWith
	}
	r, ok := f.records[id]
	if !ok || r.DeletedAt != nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	r.DeletedAt = &now
	return nil
}

func (f *fakeRepo) Publish(_ context.Context, id string) (Record, error) {
	if f.failWith != nil {
		return Record{}, f.failWith
	}
	r, ok := f.records[id]
	if !ok || r.DeletedAt != nil {
		return Record{}, ErrNotFound
	}
	r.Status = StatusPublished
	if r.PublishedAt == nil {
		now := time.Now().UTC()
		r.PublishedAt = &now
	}
	return *r, nil
}

func (f *fakeRepo) Unpublish(_ context.Context, id string) (Record, error) {
	if f.failWith != nil {
		return Record{}, f.failWith
	}
	r, ok := f.records[id]
	if !ok || r.DeletedAt != nil {
		return Record{}, ErrNotFound
	}
	r.Status = StatusUnpublished
	return *r, nil
}

// ------------------------------------------------------------------ fixtures

var fixedNow = time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

func newTestService(repo Repo) *Service {
	s := NewService(repo, validation.ApplyURLPolicy{})
	s.now = func() time.Time { return fixedNow }
	return s
}

func validInput() JobInput {
	return JobInput{
		Title:         "Senior Cloud Engineer",
		Description:   strings.Repeat("Own the platform. ", 4),
		Salary:        "$140k - $175k",
		Qualification: "B.Sc. Computer Science or equivalent experience",
		JobType:       "full-time",
		Location:      "Berlin, Germany",
		ApplyURL:      "https://example.com/careers/senior-cloud-engineer",
	}
}

func wantAppError(t *testing.T, err error, code httpx.Code) *httpx.AppError {
	t.Helper()
	appErr, ok := httpx.AsAppError(err)
	if !ok {
		t.Fatalf("error = %v, want an *AppError with code %s", err, code)
	}
	if appErr.Code != code {
		t.Fatalf("code = %s, want %s (details: %v)", appErr.Code, code, appErr.Details)
	}
	return appErr
}

func str(s string) *string { return &s }

// ------------------------------------------------------------------- create

func TestCreateDerivesSlugAndDefaultsToDraft(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	job, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if job.Slug != "senior-cloud-engineer" {
		t.Errorf("slug = %q, want senior-cloud-engineer", job.Slug)
	}
	if job.Status != StatusDraft {
		t.Errorf("status = %q, want draft by default", job.Status)
	}
	if job.PublishedAt != nil {
		t.Errorf("published_at = %v on a draft, want null", job.PublishedAt)
	}
	if job.Excerpt == "" {
		t.Error("excerpt is empty; the list payload depends on it being computed")
	}
	if job.Description == "" {
		t.Error("description is missing from the single-resource shape")
	}
}

func TestCreateDeduplicatesADerivedSlug(t *testing.T) {
	repo := newFakeRepo()
	repo.seed(Record{Slug: "senior-cloud-engineer", Status: StatusPublished})
	svc := newTestService(repo)

	job, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if job.Slug != "senior-cloud-engineer-2" {
		t.Errorf("slug = %q, want senior-cloud-engineer-2 — a derived slug is never a conflict", job.Slug)
	}
}

func TestCreateRejectsAnExplicitSlugCollision(t *testing.T) {
	repo := newFakeRepo()
	repo.seed(Record{Slug: "taken", Status: StatusDraft})
	svc := newTestService(repo)

	in := validInput()
	in.Slug = str("taken")

	_, err := svc.Create(context.Background(), in)
	wantAppError(t, err, httpx.CodeSlugConflict)
}

func TestCreateNormalisesAnExplicitSlug(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	in := validInput()
	in.Slug = str("Platform Engineer (EU)")

	job, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if job.Slug != "platform-engineer-eu" {
		t.Errorf("slug = %q, want platform-engineer-eu", job.Slug)
	}
}

func TestCreateWithStatusPublishedStampsPublishedAt(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	in := validInput()
	in.Status = str("published")

	job, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if job.PublishedAt == nil {
		t.Fatal("published_at is null after creating with status=published")
	}
	if got := job.PublishedAt.Time(); !got.Equal(fixedNow) {
		t.Errorf("published_at = %v, want %v", got, fixedNow)
	}
}

func TestCreateReportsEveryFieldFailureAtOnce(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	_, err := svc.Create(context.Background(), JobInput{
		Title:         "ab",
		Description:   "short",
		Salary:        "",
		Qualification: "",
		JobType:       "permanent",
		Location:      "",
		ApplyURL:      "javascript:alert(1)",
		Country:       str(strings.Repeat("x", 100)),
		ExpiresAt:     str("2020-01-01T00:00:00Z"),
		Status:        str("archived"),
	})

	appErr := wantAppError(t, err, httpx.CodeValidationFailed)
	for _, field := range []string{
		"title", "description", "salary", "qualification", "job_type",
		"location", "apply_url", "country", "expires_at", "status",
	} {
		if appErr.Details[field] == "" {
			t.Errorf("details is missing %q: %v", field, appErr.Details)
		}
	}
	if len(repo.createdWith) != 0 {
		t.Error("an invalid create reached the repository")
	}
}

func TestCreateRejectsAnUnsafeApplyURL(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	for _, bad := range []string{
		"javascript:alert(1)",
		"JavaScript:alert(document.cookie)",
		"data:text/html,<script>fetch('/steal')</script>",
		"http://example.com/apply", // http is off by default
		"//evil.example/apply",
		"https://user:pass@example.com/apply",
	} {
		in := validInput()
		in.ApplyURL = bad

		_, err := svc.Create(context.Background(), in)
		appErr := wantAppError(t, err, httpx.CodeValidationFailed)
		if appErr.Details["apply_url"] == "" {
			t.Errorf("apply_url %q was accepted", bad)
		}
	}
}

func TestCreateAcceptsHTTPWhenThePolicyAllowsIt(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, validation.ApplyURLPolicy{AllowHTTP: true})

	in := validInput()
	in.ApplyURL = "http://example.com/apply"

	if _, err := svc.Create(context.Background(), in); err != nil {
		t.Fatalf("Create() = %v, want http accepted with ALLOW_HTTP_APPLY_URLS on", err)
	}
}

func TestCreateStoresApplyURLByteForByte(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	const exact = "https://boards.example.com/Apply?job=42&utm_source=JobPaglu#form"
	in := validInput()
	in.ApplyURL = exact

	job, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if job.ApplyURL != exact {
		t.Errorf("apply_url = %q, want %q unchanged (contract section 7)", job.ApplyURL, exact)
	}
}

func TestCreateTreatsBlankOptionalFieldsAsNull(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	in := validInput()
	in.Company = str("   ")
	in.RemoteType = str("")

	job, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if job.Company != nil {
		t.Errorf("company = %q, want null so the column stays NULL", *job.Company)
	}
	if job.RemoteType != nil {
		t.Errorf("remote_type = %q, want null", *job.RemoteType)
	}
}

// ------------------------------------------------------------------- update

func TestUpdateKeepsTheSlugWhenTheTitleChanges(t *testing.T) {
	repo := newFakeRepo()
	existing := repo.seed(Record{
		Slug: "senior-cloud-engineer", Title: "Senior Cloud Engineer", Status: StatusPublished,
	})
	svc := newTestService(repo)

	in := validInput()
	in.Title = "Principal Platform Engineer"

	job, err := svc.Update(context.Background(), existing.ID, in)
	if err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if job.Slug != "senior-cloud-engineer" {
		t.Errorf("slug = %q; a title edit must not move a live URL (contract section 4)", job.Slug)
	}
	if job.Title != "Principal Platform Engineer" {
		t.Errorf("title = %q, want the new title", job.Title)
	}
}

func TestUpdateChangesTheSlugOnlyWhenSentExplicitly(t *testing.T) {
	repo := newFakeRepo()
	existing := repo.seed(Record{Slug: "old-slug", Status: StatusPublished})
	svc := newTestService(repo)

	in := validInput()
	in.Slug = str("new-slug")

	job, err := svc.Update(context.Background(), existing.ID, in)
	if err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if job.Slug != "new-slug" {
		t.Errorf("slug = %q, want new-slug", job.Slug)
	}
}

func TestUpdateAllowsAJobToKeepItsOwnSlug(t *testing.T) {
	repo := newFakeRepo()
	existing := repo.seed(Record{Slug: "mine", Status: StatusDraft})
	svc := newTestService(repo)

	in := validInput()
	in.Slug = str("mine")

	if _, err := svc.Update(context.Background(), existing.ID, in); err != nil {
		t.Fatalf("Update() = %v; a job must not conflict with itself", err)
	}
}

func TestUpdateKeepsTheExistingStatusWhenAbsent(t *testing.T) {
	repo := newFakeRepo()
	published := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	existing := repo.seed(Record{Slug: "live", Status: StatusPublished, PublishedAt: &published})
	svc := newTestService(repo)

	job, err := svc.Update(context.Background(), existing.ID, validInput())
	if err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if job.Status != StatusPublished {
		t.Errorf("status = %q; saving an edit must not silently unpublish a live job", job.Status)
	}
	if job.PublishedAt == nil || !job.PublishedAt.Time().Equal(published) {
		t.Errorf("published_at = %v, want the original %v", job.PublishedAt, published)
	}
}

func TestUpdateAcceptsAnAlreadyExpiredTimestamp(t *testing.T) {
	repo := newFakeRepo()
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := repo.seed(Record{Slug: "expired", Status: StatusPublished, ExpiresAt: &past})
	svc := newTestService(repo)

	in := validInput()
	in.ExpiresAt = str("2026-01-01T00:00:00Z")

	// "Must be in the future" is a create-time rule. An already-expired job that
	// cannot be edited is unfixable.
	if _, err := svc.Update(context.Background(), existing.ID, in); err != nil {
		t.Fatalf("Update() = %v, want a past expires_at accepted on update", err)
	}
}

func TestUpdateUnknownIDIsNotFound(t *testing.T) {
	svc := newTestService(newFakeRepo())
	_, err := svc.Update(context.Background(), uuidFor(999), validInput())
	wantAppError(t, err, httpx.CodeJobNotFound)
}

// ------------------------------------------------------ publish / unpublish

func TestPublishStampsPublishedAtOnceOnly(t *testing.T) {
	repo := newFakeRepo()
	record := validRecord()
	record.Status = StatusDraft
	existing := repo.seed(record)
	svc := newTestService(repo)

	first, err := svc.Publish(context.Background(), existing.ID)
	if err != nil {
		t.Fatalf("Publish() = %v", err)
	}
	if first.PublishedAt == nil {
		t.Fatal("published_at is null after the first publish")
	}
	original := first.PublishedAt.Time()

	if _, err := svc.Unpublish(context.Background(), existing.ID); err != nil {
		t.Fatalf("Unpublish() = %v", err)
	}
	second, err := svc.Publish(context.Background(), existing.ID)
	if err != nil {
		t.Fatalf("re-Publish() = %v", err)
	}
	if !second.PublishedAt.Time().Equal(original) {
		t.Errorf("published_at = %v after re-publish, want the original %v", second.PublishedAt.Time(), original)
	}
}

func TestPublishRefusesAJobThatWouldBeUnsafeWhenPublic(t *testing.T) {
	repo := newFakeRepo()
	// A row written outside this API — a manual UPDATE, an import — with an
	// apply_url that must never reach an href.
	bad := validRecord()
	bad.ApplyURL = "javascript:alert(document.cookie)"
	existing := repo.seed(bad)
	svc := newTestService(repo)

	_, err := svc.Publish(context.Background(), existing.ID)
	appErr := wantAppError(t, err, httpx.CodeValidationFailed)
	if appErr.Details["apply_url"] == "" {
		t.Errorf("details = %v, want apply_url reported", appErr.Details)
	}
	if repo.records[existing.ID].Status == StatusPublished {
		t.Error("the job was published despite failing validation")
	}
}

func TestUnpublishLeavesPublishedAtIntact(t *testing.T) {
	repo := newFakeRepo()
	published := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	record := validRecord()
	record.Status = StatusPublished
	record.PublishedAt = &published
	existing := repo.seed(record)
	svc := newTestService(repo)

	job, err := svc.Unpublish(context.Background(), existing.ID)
	if err != nil {
		t.Fatalf("Unpublish() = %v", err)
	}
	if job.Status != StatusUnpublished {
		t.Errorf("status = %q, want unpublished", job.Status)
	}
	if job.PublishedAt == nil || !job.PublishedAt.Time().Equal(published) {
		t.Errorf("published_at = %v, want %v preserved", job.PublishedAt, published)
	}
}

// -------------------------------------------------------------------- delete

func TestDeleteIsSoftAndIdempotentlyNotFound(t *testing.T) {
	repo := newFakeRepo()
	existing := repo.seed(validRecord())
	svc := newTestService(repo)

	if err := svc.Delete(context.Background(), existing.ID); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if repo.records[existing.ID] == nil {
		t.Fatal("the row was removed; the delete must be soft")
	}
	if repo.records[existing.ID].DeletedAt == nil {
		t.Error("deleted_at was not set")
	}

	// A second delete is a 404, not a success and not a 500.
	wantAppError(t, svc.Delete(context.Background(), existing.ID), httpx.CodeJobNotFound)
}

// ------------------------------------------------------------------- reading

func TestListPublicPaginates(t *testing.T) {
	repo := newFakeRepo()
	for range 3 {
		record := validRecord()
		record.Status = StatusPublished
		repo.seed(record)
	}
	svc := newTestService(repo)

	summaries, meta, err := svc.ListPublic(context.Background(), ListQuery{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("ListPublic() = %v", err)
	}
	if meta.Page != 1 || meta.PerPage != 2 {
		t.Errorf("meta = %+v, want page 1 per_page 2", meta)
	}
	if summaries == nil {
		t.Error("summaries is nil; the envelope must carry [] rather than null")
	}
}

func TestListQueryClamping(t *testing.T) {
	tests := []struct {
		name        string
		query       ListQuery
		maxPerPage  int
		wantPage    int
		wantPerPage int
	}{
		{"defaults", ListQuery{}, MaxPerPage, 1, DefaultPerPage},
		{"page below one is clamped", ListQuery{Page: -5}, MaxPerPage, 1, DefaultPerPage},
		{"per_page over the public cap", ListQuery{Page: 2, PerPage: 500}, MaxPerPage, 2, MaxPerPage},
		{"per_page over the admin cap", ListQuery{Page: 1, PerPage: 500}, AdminPerPage, 1, AdminPerPage},
		{"per_page of zero falls back", ListQuery{PerPage: 0}, MaxPerPage, 1, DefaultPerPage},
		{"per_page negative falls back", ListQuery{PerPage: -3}, MaxPerPage, 1, DefaultPerPage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, perPage := tc.query.normalise(tc.maxPerPage)
			if page != tc.wantPage || perPage != tc.wantPerPage {
				t.Errorf("normalise() = (%d, %d), want (%d, %d)", page, perPage, tc.wantPage, tc.wantPerPage)
			}
		})
	}
}

func TestOffsetIsDerivedFromPage(t *testing.T) {
	filter := ListQuery{}.filter(3, 20)
	if filter.Offset != 40 {
		t.Errorf("offset = %d for page 3 of 20, want 40", filter.Offset)
	}
	if filter.Limit != 20 {
		t.Errorf("limit = %d, want 20", filter.Limit)
	}
}

func TestPaginationTotalPages(t *testing.T) {
	tests := []struct {
		total   int64
		perPage int
		want    int
	}{
		{0, 20, 0}, // empty is 0 pages, not 1 (contract section 2.3)
		{1, 20, 1},
		{20, 20, 1},
		{21, 20, 2},
		{42, 20, 3},
	}
	for _, tc := range tests {
		if got := NewPagination(1, tc.perPage, tc.total).TotalPages; got != tc.want {
			t.Errorf("NewPagination(_, %d, %d).TotalPages = %d, want %d", tc.perPage, tc.total, got, tc.want)
		}
	}
}

func TestGetPublicHidesEverythingButPublished(t *testing.T) {
	repo := newFakeRepo()
	for _, status := range []JobStatus{StatusDraft, StatusUnpublished} {
		record := validRecord()
		record.Slug = "hidden-" + string(status)
		record.Status = status
		repo.seed(record)
	}
	svc := newTestService(repo)

	for _, slug := range []string{"hidden-draft", "hidden-unpublished", "does-not-exist"} {
		_, err := svc.GetPublic(context.Background(), slug)
		wantAppError(t, err, httpx.CodeJobNotFound)
	}
}

// ------------------------------------------------------------ error mapping

func TestRepositoryFailuresMapToTheRightCode(t *testing.T) {
	tests := []struct {
		name     string
		repoErr  error
		wantCode httpx.Code
	}{
		{"unavailable becomes 503", ErrUnavailable, httpx.CodeServiceUnavailable},
		{"not found becomes 404", ErrNotFound, httpx.CodeJobNotFound},
		{"slug conflict becomes 409", ErrSlugConflict, httpx.CodeSlugConflict},
		{"an unrecognised error becomes 500", errors.New("scan: unexpected NULL"), httpx.CodeInternalError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			repo.failWith = tc.repoErr
			svc := newTestService(repo)

			_, _, err := svc.ListPublic(context.Background(), ListQuery{})
			wantAppError(t, err, tc.wantCode)
		})
	}
}

// A database outage must not surface as a 500, and must not leak the driver
// error into the response.
func TestOutageBecomesAServiceUnavailableWithNoDetail(t *testing.T) {
	repo := newFakeRepo()
	repo.failWith = errors.Join(ErrUnavailable, errors.New("dial tcp 10.0.0.1:5432: connect: connection refused"))
	svc := newTestService(repo)

	_, err := svc.GetPublic(context.Background(), "anything")
	appErr := wantAppError(t, err, httpx.CodeServiceUnavailable)
	if strings.Contains(appErr.Message, "5432") || strings.Contains(appErr.Message, "dial") {
		t.Errorf("message leaks the driver error: %q", appErr.Message)
	}
}

// ------------------------------------------------------------------- helpers

func validRecord() Record {
	in := validInput()
	return Record{
		Slug:          "senior-cloud-engineer",
		Title:         in.Title,
		Description:   in.Description,
		Salary:        in.Salary,
		Qualification: in.Qualification,
		JobType:       TypeFullTime,
		Location:      in.Location,
		ApplyURL:      in.ApplyURL,
		Status:        StatusDraft,
	}
}
