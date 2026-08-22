// Package validation holds the field rules from API-CONTRACT.md section 9 and
// the apply-URL allowlist from section 7.
//
// Two properties the callers depend on:
//
//   - Every invalid field is reported, not just the first. A form that fixes one
//     error per round trip is a bad form.
//   - Cleaning happens before checking, so a title of "   " fails the required
//     rule rather than passing a length check on whitespace.
package validation

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Errors maps a field name to one human-readable sentence, which is exactly the
// `details` shape in the error envelope.
type Errors map[string]string

// New returns an empty error set.
func New() Errors { return make(Errors) }

// Add records a message for a field. The first message for a field wins, so a
// specific rule checked early is not overwritten by a generic one later.
func (e Errors) Add(field, message string) {
	if _, exists := e[field]; !exists {
		e[field] = message
	}
}

// Any reports whether anything failed.
func (e Errors) Any() bool { return len(e) > 0 }

// Map returns the details map for the response, or nil when there is nothing to
// report.
func (e Errors) Map() map[string]string {
	if len(e) == 0 {
		return nil
	}
	return e
}

// ---------------------------------------------------------------- cleaning

// CleanSingleLine trims the value and removes every ASCII control character.
//
// Control characters in a title are either a copy-paste accident or an attempt
// to smuggle a terminal escape into a log line; neither is worth preserving.
func CleanSingleLine(s string) string {
	return strings.TrimSpace(stripControl(s, false))
}

// CleanMultiline is CleanSingleLine but keeps \n and \t, which a job
// description legitimately contains. CRLF is normalised to LF first so a
// Windows paste does not double the apparent length.
func CleanMultiline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(stripControl(s, true))
}

func stripControl(s string, keepNewlineAndTab bool) string {
	// Fast path: most values are clean, so avoid allocating.
	clean := true
	for _, r := range s {
		if isStrippable(r, keepNewlineAndTab) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isStrippable(r, keepNewlineAndTab) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isStrippable(r rune, keepNewlineAndTab bool) bool {
	if keepNewlineAndTab && (r == '\n' || r == '\t') {
		return false
	}
	// C0 controls, DEL, and the C1 range, which includes the bidi and
	// line-separator characters that render unpredictably.
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// Length counts characters, not bytes. The contract says "chars", and a
// 160-byte limit would silently cut a Japanese job title to 53 of them.
func Length(s string) int { return utf8.RuneCountInString(s) }

// ---------------------------------------------------------------- validators
//
// Each validator returns the cleaned value so a caller can build the record from
// what was checked rather than from the raw input.

// RequiredString cleans, then enforces a rune-length range.
func RequiredString(e Errors, field, raw string, min, max int) string {
	value := CleanSingleLine(raw)
	checkRequiredLength(e, field, value, min, max)
	return value
}

// RequiredText is RequiredString for a field that may contain newlines.
func RequiredText(e Errors, field, raw string, min, max int) string {
	value := CleanMultiline(raw)
	checkRequiredLength(e, field, value, min, max)
	return value
}

func checkRequiredLength(e Errors, field, value string, min, max int) {
	switch n := Length(value); {
	case n == 0:
		e.Add(field, "This field is required.")
	case n < min || n > max:
		if min == 1 {
			e.Add(field, fmt.Sprintf("Must be at most %d characters.", max))
		} else {
			e.Add(field, fmt.Sprintf("Must be between %d and %d characters.", min, max))
		}
	}
}

// OptionalString cleans an optional value and enforces a maximum.
//
// It returns nil for an absent or empty value, which is what the nullable
// columns and the `string | null` JSON fields both want. An empty string and an
// omitted field are deliberately the same thing: a form that posts "" for an
// untouched optional input should not store an empty string.
func OptionalString(e Errors, field string, raw *string, max int) *string {
	if raw == nil {
		return nil
	}
	value := CleanSingleLine(*raw)
	if value == "" {
		return nil
	}
	if n := Length(value); n > max {
		e.Add(field, fmt.Sprintf("Must be at most %d characters.", max))
	}
	return &value
}

// RequiredEnum checks membership in a closed set.
func RequiredEnum(e Errors, field, raw string, allowed []string) string {
	value := CleanSingleLine(raw)
	if value == "" {
		e.Add(field, "This field is required.")
		return value
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	e.Add(field, "Must be one of: "+strings.Join(allowed, ", ")+".")
	return value
}

// OptionalEnum is RequiredEnum for a nullable column.
func OptionalEnum(e Errors, field string, raw *string, allowed []string) *string {
	if raw == nil {
		return nil
	}
	value := CleanSingleLine(*raw)
	if value == "" {
		return nil
	}
	for _, candidate := range allowed {
		if value == candidate {
			return &value
		}
	}
	e.Add(field, "Must be one of: "+strings.Join(allowed, ", ")+".")
	return nil
}

// FutureTimestamp parses an optional RFC 3339 timestamp that must not already
// have passed.
//
// `now` is a parameter rather than a call to time.Now so the rule is testable
// without sleeping, and so a create and its validation agree on "now".
func FutureTimestamp(e Errors, field string, raw *string, now time.Time) *time.Time {
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
	if !parsed.After(now) {
		e.Add(field, "Must be in the future.")
		return nil
	}
	utc := parsed.UTC()
	return &utc
}

// SlugFormat validates an explicitly supplied slug against the contract's
// pattern: lowercase alphanumeric groups joined by single hyphens.
//
// Written as a scan rather than a regexp so the rule is readable and there is no
// package-level regexp to keep in step with the prose.
func SlugFormat(e Errors, field, value string) bool {
	n := Length(value)
	if n == 0 {
		e.Add(field, "This field is required.")
		return false
	}
	if n > 100 {
		e.Add(field, "Must be at most 100 characters.")
		return false
	}

	const message = "Must contain only lowercase letters, numbers and single hyphens."
	if value[0] == '-' || value[len(value)-1] == '-' {
		e.Add(field, message)
		return false
	}
	previousHyphen := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			previousHyphen = false
		case c == '-':
			if previousHyphen {
				e.Add(field, message)
				return false
			}
			previousHyphen = true
		default:
			e.Add(field, message)
			return false
		}
	}
	return true
}

// IsUUID reports whether s is a canonical 8-4-4-4-12 lowercase-or-uppercase hex
// UUID.
//
// Route parameters are checked with this before reaching SQL, so an unparseable
// id is a 400 from the handler rather than a driver error from the database
// (contract section 6).
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// Email applies a deliberately loose check.
//
// Strict RFC 5322 conformance is not the goal: the only consumer is the admin
// login form, where the real validation is "does a row with this address exist".
// Rejecting an unusual but legal address would lock out a real administrator,
// which is worse than accepting a nonsense one that then fails to authenticate.
func Email(e Errors, field, raw string) string {
	value := strings.ToLower(CleanSingleLine(raw))
	switch {
	case value == "":
		e.Add(field, "This field is required.")
	case Length(value) > 254:
		e.Add(field, "Must be at most 254 characters.")
	default:
		at := strings.LastIndex(value, "@")
		local, domain := "", ""
		if at >= 0 {
			local, domain = value[:at], value[at+1:]
		}
		if at < 0 || local == "" || domain == "" || !strings.Contains(domain, ".") ||
			strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") ||
			strings.ContainsAny(value, " \t") {
			e.Add(field, "Must be a valid email address.")
		}
	}
	return value
}
