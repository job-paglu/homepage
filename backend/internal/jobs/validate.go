package jobs

import (
	"strings"
	"time"

	"github.com/job-paglu/homepage/backend/internal/validation"
)

// Field bounds, transcribed from API-CONTRACT.md section 9. Named constants
// rather than literals at the call site so the contract table and the code can
// be compared line by line.
const (
	titleMin, titleMax             = 3, 160
	descriptionMin, descriptionMax = 20, 20000
	salaryMax                      = 120
	qualificationMax               = 240
	locationMax                    = 160
	companyMax                     = 160
	countryMax                     = 80
	experienceLevelMax             = 80
	categoryMax                    = 80
)

// validateInput checks a create or update body and returns the record it
// describes alongside every field failure found.
//
// existing is nil for a create. Two rules depend on it: expires_at only has to
// be in the future on create (contract section 9), and an absent status keeps
// the current one on update rather than resetting to draft.
//
// Slug is not set here. It needs a database round trip to settle, which is the
// service's job, not a validator's.
func (s *Service) validateInput(in JobInput, existing *Record, now time.Time) (Record, validation.Errors) {
	e := validation.New()
	var record Record

	record.Title = validation.RequiredString(e, "title", in.Title, titleMin, titleMax)
	record.Description = validation.RequiredText(e, "description", in.Description, descriptionMin, descriptionMax)
	record.Salary = validation.RequiredString(e, "salary", in.Salary, 1, salaryMax)
	record.Qualification = validation.RequiredString(e, "qualification", in.Qualification, 1, qualificationMax)
	record.Location = validation.RequiredString(e, "location", in.Location, 1, locationMax)
	record.JobType = JobType(validation.RequiredEnum(e, "job_type", in.JobType, TypeValues))

	// Only the outer whitespace is removed, which section 9 requires of every
	// string field. Everything inside is left alone and the validator rejects it,
	// because a URL with a space in the middle is not a URL and guessing what the
	// admin meant would break the byte-for-byte storage guarantee.
	record.ApplyURL = validation.ApplyURL(e, "apply_url", strings.TrimSpace(in.ApplyURL), s.applyPolicy)

	record.Company = validation.OptionalString(e, "company", in.Company, companyMax)
	record.Country = validation.OptionalString(e, "country", in.Country, countryMax)
	record.ExperienceLevel = validation.OptionalString(e, "experience_level", in.ExperienceLevel, experienceLevelMax)
	record.Category = validation.OptionalString(e, "category", in.Category, categoryMax)

	if remote := validation.OptionalEnum(e, "remote_type", in.RemoteType, RemoteValues); remote != nil {
		value := RemoteType(*remote)
		record.RemoteType = &value
	}

	if in.IsFeatured != nil {
		record.IsFeatured = *in.IsFeatured
	}

	if existing == nil {
		record.ExpiresAt = validation.FutureTimestamp(e, "expires_at", in.ExpiresAt, now)
	} else {
		// On update the rule is format only. Requiring a future value would make
		// an already-expired job impossible to edit, which is the opposite of
		// what an admin needs at that moment.
		record.ExpiresAt = parseTimestamp(e, "expires_at", in.ExpiresAt)
	}

	switch {
	case in.Status != nil && strings.TrimSpace(*in.Status) != "":
		record.Status = JobStatus(validation.RequiredEnum(e, "status", *in.Status, StatusValues))
	case existing != nil:
		record.Status = existing.Status
	default:
		record.Status = StatusDraft
	}

	// An explicitly supplied slug is normalised the same way a derived one is
	// (contract section 4), then checked against the format rule. Normalising
	// first means "Senior Cloud Engineer" typed into the slug field works
	// instead of failing a pattern match.
	if in.Slug != nil {
		if normalised := NormaliseSlug(*in.Slug); normalised == "" {
			e.Add("slug", "Must contain only lowercase letters, numbers and single hyphens.")
		} else {
			validation.SlugFormat(e, "slug", normalised)
		}
	}

	return record, e
}

// validateStored re-checks a row that is already in the database, before it
// becomes publicly visible.
//
// The row may have been written by something other than this API — a manual
// UPDATE, a restored backup, a future import script. Publishing is the moment
// that matters, so it is the moment to check.
func (s *Service) validateStored(r Record) validation.Errors {
	e := validation.New()

	validation.RequiredString(e, "title", r.Title, titleMin, titleMax)
	validation.RequiredText(e, "description", r.Description, descriptionMin, descriptionMax)
	validation.RequiredString(e, "salary", r.Salary, 1, salaryMax)
	validation.RequiredString(e, "qualification", r.Qualification, 1, qualificationMax)
	validation.RequiredString(e, "location", r.Location, 1, locationMax)
	validation.RequiredEnum(e, "job_type", string(r.JobType), TypeValues)
	validation.ApplyURL(e, "apply_url", r.ApplyURL, s.applyPolicy)

	return e
}

// parseTimestamp accepts an optional RFC 3339 value with no recency rule.
func parseTimestamp(e validation.Errors, field string, raw *string) *time.Time {
	if raw == nil {
		return nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		e.Add(field, "Must be an RFC 3339 timestamp, for example 2026-12-31T23:59:59Z.")
		return nil
	}
	utc := parsed.UTC()
	return &utc
}
