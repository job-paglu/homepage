// Package jobs owns the job posting: its shape on the wire, its rules, and the
// SQL that stores it.
//
// The three layers inside are kept strictly separate, because mixing them is
// what turns a small CRUD service into an untestable one:
//
//	handlers_*.go   HTTP only — parse, delegate, respond. No SQL, no rules.
//	service.go      rules only — validation, slugs, state transitions. No SQL,
//	                no http.Request; it talks to a Repo interface, which is what
//	                makes service_test.go possible without Postgres.
//	repository.go   SQL only. Every statement parameterised.
package jobs

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

// ------------------------------------------------------------- enumerations
//
// Stored as text with a CHECK constraint rather than a Postgres enum, so adding
// a member later is an ALTER ... CHECK instead of a type migration (contract
// section 2.4).

// JobStatus is the publication state of a posting.
type JobStatus string

// The complete set of statuses.
const (
	StatusDraft       JobStatus = "draft"
	StatusPublished   JobStatus = "published"
	StatusUnpublished JobStatus = "unpublished"
)

// JobType is the employment arrangement.
type JobType string

// The complete set of job types.
const (
	TypeFullTime   JobType = "full-time"
	TypePartTime   JobType = "part-time"
	TypeContract   JobType = "contract"
	TypeInternship JobType = "internship"
	TypeFreelance  JobType = "freelance"
	TypeTemporary  JobType = "temporary"
)

// RemoteType is where the work happens.
type RemoteType string

// The complete set of remote types.
const (
	RemoteRemote RemoteType = "remote"
	RemoteHybrid RemoteType = "hybrid"
	RemoteOnsite RemoteType = "onsite"
)

// StatusValues, TypeValues and RemoteValues are the allowlists handed to the
// validators. Order matters only because it is what a user sees in an error
// message.
var (
	StatusValues = []string{string(StatusDraft), string(StatusPublished), string(StatusUnpublished)}
	TypeValues   = []string{
		string(TypeFullTime), string(TypePartTime), string(TypeContract),
		string(TypeInternship), string(TypeFreelance), string(TypeTemporary),
	}
	RemoteValues = []string{string(RemoteRemote), string(RemoteHybrid), string(RemoteOnsite)}
)

// ---------------------------------------------------------------- timestamps

// Timestamp marshals as RFC 3339 in UTC with a Z suffix, which is what the
// contract promises everywhere.
//
// time.Time's own marshaller emits RFC 3339 *with nanoseconds and the stored
// offset*, so a value read back from Postgres as +00:00 would serialise as
// "2026-08-21T09:41:07.123456+00:00" — three ways different from the contract.
// Wrapping the type fixes that once rather than at every field.
type Timestamp time.Time

// MarshalJSON implements json.Marshaler.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).UTC().Format(time.RFC3339))
}

// UnmarshalJSON implements json.Unmarshaler, so a response can be decoded in a
// test without a second type.
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return err
	}
	*t = Timestamp(parsed.UTC())
	return nil
}

// Time returns the underlying time.Time.
func (t Timestamp) Time() time.Time { return time.Time(t) }

func stamp(t time.Time) Timestamp { return Timestamp(t.UTC()) }

func stampPtr(t *time.Time) *Timestamp {
	if t == nil {
		return nil
	}
	s := stamp(*t)
	return &s
}

// ------------------------------------------------------------------- record

// Record is a row of `jobs` as the repository reads and writes it.
//
// It is the internal representation, deliberately separate from the JSON DTOs:
// it carries deleted_at, which no response may ever include, and it does not
// carry excerpt, which is computed rather than stored.
type Record struct {
	ID              string
	Slug            string
	Title           string
	Description     string
	Salary          string
	Qualification   string
	JobType         JobType
	Location        string
	ApplyURL        string
	Company         *string
	Country         *string
	RemoteType      *RemoteType
	ExperienceLevel *string
	Category        *string
	IsFeatured      bool
	Status          JobStatus
	PublishedAt     *time.Time
	ExpiresAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
}

// --------------------------------------------------------------------- DTOs

// JobSummary is the list-endpoint shape from contract section 2.1.
//
// Field order matches frontend/src/lib/types.ts so a diff between the two files
// is readable. Nullability matches it exactly: a pointer here is a `| null`
// there, and a value here is never null there.
type JobSummary struct {
	ID              string      `json:"id"`
	Slug            string      `json:"slug"`
	Title           string      `json:"title"`
	Excerpt         string      `json:"excerpt"`
	Salary          string      `json:"salary"`
	Qualification   string      `json:"qualification"`
	JobType         JobType     `json:"job_type"`
	Location        string      `json:"location"`
	Company         *string     `json:"company"`
	Country         *string     `json:"country"`
	RemoteType      *RemoteType `json:"remote_type"`
	ExperienceLevel *string     `json:"experience_level"`
	Category        *string     `json:"category"`
	IsFeatured      bool        `json:"is_featured"`
	ApplyURL        string      `json:"apply_url"`
	Status          JobStatus   `json:"status"`
	PublishedAt     *Timestamp  `json:"published_at"`
	ExpiresAt       *Timestamp  `json:"expires_at"`
	CreatedAt       Timestamp   `json:"created_at"`
	UpdatedAt       Timestamp   `json:"updated_at"`
}

// Job is the single-resource shape: every summary field plus the full
// description (contract section 2.2).
//
// The embedded struct flattens in JSON, so the wire format is one object and
// `description` lands last, exactly as the contract's example shows.
type Job struct {
	JobSummary
	// Description is plain text with newlines, never HTML. A renderer must
	// escape it (contract section 2.2).
	Description string `json:"description"`
}

// Pagination is the `meta` object from contract section 2.3.
type Pagination struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// NewPagination computes total_pages, which is 0 — not 1 — for an empty result.
func NewPagination(page, perPage int, total int64) Pagination {
	totalPages := 0
	if total > 0 && perPage > 0 {
		totalPages = int((total + int64(perPage) - 1) / int64(perPage))
	}
	return Pagination{Page: page, PerPage: perPage, Total: total, TotalPages: totalPages}
}

// Summary converts a row to the list DTO. DeletedAt is dropped here, which is
// the reason the conversion exists at all.
func (r Record) Summary() JobSummary {
	return JobSummary{
		ID:              r.ID,
		Slug:            r.Slug,
		Title:           r.Title,
		Excerpt:         Excerpt(r.Description, ExcerptLen),
		Salary:          r.Salary,
		Qualification:   r.Qualification,
		JobType:         r.JobType,
		Location:        r.Location,
		Company:         r.Company,
		Country:         r.Country,
		RemoteType:      r.RemoteType,
		ExperienceLevel: r.ExperienceLevel,
		Category:        r.Category,
		IsFeatured:      r.IsFeatured,
		ApplyURL:        r.ApplyURL,
		Status:          r.Status,
		PublishedAt:     stampPtr(r.PublishedAt),
		ExpiresAt:       stampPtr(r.ExpiresAt),
		CreatedAt:       stamp(r.CreatedAt),
		UpdatedAt:       stamp(r.UpdatedAt),
	}
}

// Full converts a row to the single-resource DTO.
func (r Record) Full() Job {
	return Job{JobSummary: r.Summary(), Description: r.Description}
}

// Summaries converts a page of rows, always returning a non-nil slice so the
// envelope carries `[]` rather than `null` for an empty result.
func Summaries(records []Record) []JobSummary {
	out := make([]JobSummary, 0, len(records))
	for _, record := range records {
		out = append(out, record.Summary())
	}
	return out
}

// --------------------------------------------------------------------- input

// JobInput is the POST/PUT body (contract section 4).
//
// The optional fields are pointers so "absent" and "null" are distinguishable
// from "empty string". That distinction only actually matters for Slug — absent
// means keep the existing one on update — but making the rest consistent avoids
// a reader having to remember which is which.
type JobInput struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	Salary        string `json:"salary"`
	Qualification string `json:"qualification"`
	JobType       string `json:"job_type"`
	Location      string `json:"location"`
	ApplyURL      string `json:"apply_url"`

	Company         *string `json:"company"`
	Country         *string `json:"country"`
	RemoteType      *string `json:"remote_type"`
	ExperienceLevel *string `json:"experience_level"`
	Category        *string `json:"category"`
	IsFeatured      *bool   `json:"is_featured"`
	ExpiresAt       *string `json:"expires_at"`
	Status          *string `json:"status"`
	// Slug omitted on create derives from the title; omitted on update keeps the
	// existing slug, which is what protects a live URL from a title edit.
	Slug *string `json:"slug"`
}

// -------------------------------------------------------------------- excerpt

// ExcerptLen is the character budget from contract section 2.1.
const ExcerptLen = 200

// Excerpt returns the first max characters of a description, cut on a word
// boundary, with an ellipsis when it was truncated.
//
// Internal whitespace is collapsed to single spaces first. A description is
// plain text with paragraph breaks, and an excerpt that begins with two blank
// lines looks like a rendering bug in a card that has no room for them.
func Excerpt(description string, max int) string {
	collapsed := collapseWhitespace(description)
	runes := []rune(collapsed)
	if len(runes) <= max {
		return collapsed
	}

	window := runes[:max]
	// Walk back to the last space so a word is not sliced in half. If the whole
	// window is one token — a URL, or a CJK sentence with no spaces — there is
	// no boundary to find and a hard cut is the only option.
	if idx := lastSpace(window); idx > 0 {
		window = window[:idx]
	}
	return strings.TrimRight(string(window), " ") + "…"
}

func lastSpace(runes []rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == ' ' {
			return i
		}
	}
	return -1
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			pendingSpace = b.Len() > 0
			continue
		}
		if pendingSpace {
			b.WriteRune(' ')
			pendingSpace = false
		}
		b.WriteRune(r)
	}
	return b.String()
}
