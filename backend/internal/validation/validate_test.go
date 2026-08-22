package validation

import (
	"strings"
	"testing"
	"time"
)

// The boundaries transcribed from contract section 9. Every required string
// field is exercised at min-1, min, max and max+1.
func TestRequiredStringBoundaries(t *testing.T) {
	tests := []struct {
		field   string
		min     int
		max     int
		length  int
		wantErr bool
	}{
		{"title", 3, 160, 2, true},
		{"title", 3, 160, 3, false},
		{"title", 3, 160, 160, false},
		{"title", 3, 160, 161, true},
		{"salary", 1, 120, 1, false},
		{"salary", 1, 120, 120, false},
		{"salary", 1, 120, 121, true},
		{"qualification", 1, 240, 240, false},
		{"qualification", 1, 240, 241, true},
		{"location", 1, 160, 160, false},
		{"location", 1, 160, 161, true},
	}

	for _, tc := range tests {
		e := New()
		RequiredString(e, tc.field, strings.Repeat("x", tc.length), tc.min, tc.max)
		if got := e.Any(); got != tc.wantErr {
			t.Errorf("%s at length %d: error=%v, want %v (%v)", tc.field, tc.length, got, tc.wantErr, e.Map())
		}
	}
}

func TestRequiredTextBoundaries(t *testing.T) {
	// description: 20..20000 after trim.
	for _, tc := range []struct {
		length  int
		wantErr bool
	}{{19, true}, {20, false}, {20000, false}, {20001, true}} {
		e := New()
		RequiredText(e, "description", strings.Repeat("x", tc.length), 20, 20000)
		if got := e.Any(); got != tc.wantErr {
			t.Errorf("description at length %d: error=%v, want %v", tc.length, got, tc.wantErr)
		}
	}
}

func TestRequiredStringTrimsBeforeChecking(t *testing.T) {
	e := New()
	got := RequiredString(e, "title", "   Senior Cloud Engineer   ", 3, 160)

	if got != "Senior Cloud Engineer" {
		t.Errorf("value = %q, want the trimmed form", got)
	}
	if e.Any() {
		t.Errorf("unexpected errors: %v", e.Map())
	}

	// Whitespace-only must fail "required", not pass a length check on spaces.
	blank := New()
	RequiredString(blank, "title", "      ", 3, 160)
	if blank["title"] != "This field is required." {
		t.Errorf("details[title] = %q, want the required message", blank["title"])
	}
}

func TestLengthCountsCharactersNotBytes(t *testing.T) {
	// Three runes, nine bytes. A byte-based limit would treat this as nine.
	const cjk = "東京都"
	if got := Length(cjk); got != 3 {
		t.Errorf("Length(%q) = %d, want 3", cjk, got)
	}

	// A title of 160 multi-byte characters is legal; a byte cap would reject it.
	e := New()
	RequiredString(e, "title", strings.Repeat("é", 160), 3, 160)
	if e.Any() {
		t.Errorf("160 accented characters rejected: %v", e.Map())
	}
}

func TestControlCharacterStripping(t *testing.T) {
	t.Run("single line strips newlines and tabs", func(t *testing.T) {
		got := CleanSingleLine("Senior\nCloud\tEngineer\x07")
		if got != "SeniorCloudEngineer" {
			t.Errorf("CleanSingleLine() = %q, want control characters removed", got)
		}
	})

	t.Run("multiline keeps newlines and tabs", func(t *testing.T) {
		got := CleanMultiline("Line one\n\tIndented\x00\x1b[31m")
		want := "Line one\n\tIndented[31m"
		if got != want {
			t.Errorf("CleanMultiline() = %q, want %q — \\n and \\t survive, ESC does not", got, want)
		}
	})

	t.Run("multiline normalises CRLF", func(t *testing.T) {
		if got := CleanMultiline("a\r\nb\rc"); got != "a\nb\nc" {
			t.Errorf("CleanMultiline() = %q, want a\\nb\\nc", got)
		}
	})

	t.Run("clean input is returned untouched", func(t *testing.T) {
		const clean = "Berlin, Germany"
		if got := CleanSingleLine(clean); got != clean {
			t.Errorf("CleanSingleLine(%q) = %q", clean, got)
		}
	})
}

func TestOptionalString(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		e := New()
		if got := OptionalString(e, "company", nil, 160); got != nil {
			t.Errorf("OptionalString(nil) = %v, want nil", *got)
		}
	})

	t.Run("empty and whitespace collapse to nil", func(t *testing.T) {
		for _, raw := range []string{"", "   ", "\t"} {
			e := New()
			value := raw
			if got := OptionalString(e, "company", &value, 160); got != nil {
				t.Errorf("OptionalString(%q) = %q, want nil so the column stays NULL", raw, *got)
			}
			if e.Any() {
				t.Errorf("OptionalString(%q) reported an error: %v", raw, e.Map())
			}
		}
	})

	t.Run("boundaries from section 9", func(t *testing.T) {
		for _, tc := range []struct {
			field   string
			max     int
			length  int
			wantErr bool
		}{
			{"company", 160, 160, false},
			{"company", 160, 161, true},
			{"country", 80, 80, false},
			{"country", 80, 81, true},
			{"experience_level", 80, 81, true},
			{"category", 80, 81, true},
		} {
			e := New()
			value := strings.Repeat("x", tc.length)
			OptionalString(e, tc.field, &value, tc.max)
			if got := e.Any(); got != tc.wantErr {
				t.Errorf("%s at length %d: error=%v, want %v", tc.field, tc.length, got, tc.wantErr)
			}
		}
	})
}

func TestEnums(t *testing.T) {
	jobTypes := []string{"full-time", "part-time", "contract", "internship", "freelance", "temporary"}
	remoteTypes := []string{"remote", "hybrid", "onsite"}

	t.Run("every contract job type is accepted", func(t *testing.T) {
		for _, valid := range jobTypes {
			e := New()
			if got := RequiredEnum(e, "job_type", valid, jobTypes); got != valid || e.Any() {
				t.Errorf("RequiredEnum(%q) rejected: %v", valid, e.Map())
			}
		}
	})

	t.Run("unknown and near-miss values are rejected", func(t *testing.T) {
		for _, invalid := range []string{"fulltime", "Full-Time", "permanent", "full time", "FULL-TIME"} {
			e := New()
			RequiredEnum(e, "job_type", invalid, jobTypes)
			if !e.Any() {
				t.Errorf("RequiredEnum(%q) accepted an unknown member", invalid)
			}
		}
	})

	t.Run("required enum rejects empty", func(t *testing.T) {
		e := New()
		RequiredEnum(e, "job_type", "", jobTypes)
		if e["job_type"] != "This field is required." {
			t.Errorf("details = %q, want the required message", e["job_type"])
		}
	})

	t.Run("optional enum treats empty as null", func(t *testing.T) {
		for _, raw := range []string{"", "  "} {
			e := New()
			value := raw
			if got := OptionalEnum(e, "remote_type", &value, remoteTypes); got != nil {
				t.Errorf("OptionalEnum(%q) = %q, want nil", raw, *got)
			}
			if e.Any() {
				t.Errorf("OptionalEnum(%q) errored: %v", raw, e.Map())
			}
		}
	})

	t.Run("optional enum rejects an unknown member", func(t *testing.T) {
		e := New()
		value := "anywhere"
		if got := OptionalEnum(e, "remote_type", &value, remoteTypes); got != nil {
			t.Errorf("OptionalEnum returned %q for an invalid value", *got)
		}
		if !e.Any() {
			t.Error("OptionalEnum accepted an unknown remote_type")
		}
	})
}

func TestFutureTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		raw     *string
		wantErr bool
		wantNil bool
	}{
		{name: "absent", raw: nil, wantNil: true},
		{name: "empty", raw: ptr(""), wantNil: true},
		{name: "future", raw: ptr("2026-12-31T23:59:59Z")},
		{name: "one second in the future", raw: ptr("2026-08-21T12:00:01Z")},
		{name: "exactly now is not the future", raw: ptr("2026-08-21T12:00:00Z"), wantErr: true, wantNil: true},
		{name: "past", raw: ptr("2020-01-01T00:00:00Z"), wantErr: true, wantNil: true},
		{name: "not RFC 3339", raw: ptr("31/12/2026"), wantErr: true, wantNil: true},
		{name: "date only", raw: ptr("2026-12-31"), wantErr: true, wantNil: true},
		{name: "offset is normalised to UTC", raw: ptr("2026-12-31T23:59:59+05:30")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			got := FutureTimestamp(e, "expires_at", tc.raw, now)

			if e.Any() != tc.wantErr {
				t.Errorf("error=%v, want %v (%v)", e.Any(), tc.wantErr, e.Map())
			}
			if (got == nil) != tc.wantNil {
				t.Errorf("value nil=%v, want nil=%v", got == nil, tc.wantNil)
			}
			if got != nil && got.Location() != time.UTC {
				t.Errorf("timestamp is not UTC: %v — the API never emits a local offset", got)
			}
		})
	}
}

func TestSlugFormat(t *testing.T) {
	tests := []struct {
		slug string
		want bool
	}{
		{"senior-cloud-engineer", true},
		{"a", true},
		{"job-2", true},
		{"2026-intern", true},
		{strings.Repeat("a", 100), true},
		{strings.Repeat("a", 101), false},
		{"", false},
		{"Senior-Cloud", false},  // uppercase
		{"senior_cloud", false},  // underscore
		{"senior--cloud", false}, // double hyphen
		{"-senior", false},       // leading hyphen
		{"senior-", false},       // trailing hyphen
		{"senior cloud", false},  // space
		{"senior/cloud", false},  // path separator
		{"señor", false},         // non-ASCII
		{"../../etc/passwd", false},
	}

	for _, tc := range tests {
		e := New()
		got := SlugFormat(e, "slug", tc.slug)
		if got != tc.want {
			t.Errorf("SlugFormat(%q) = %v, want %v", tc.slug, got, tc.want)
		}
		if got == e.Any() {
			t.Errorf("SlugFormat(%q) returned %v but recorded errors %v", tc.slug, got, e.Map())
		}
	}
}

func TestIsUUID(t *testing.T) {
	valid := []string{
		"8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33",
		"8F14E45F-CEEA-467A-9C5B-2B1A0E1C9D33",
		"00000000-0000-0000-0000-000000000000",
	}
	for _, s := range valid {
		if !IsUUID(s) {
			t.Errorf("IsUUID(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",
		"not-a-uuid",
		"8f14e45f-ceea-467a-9c5b-2b1a0e1c9d3",   // one short
		"8f14e45f-ceea-467a-9c5b-2b1a0e1c9d333", // one long
		"8f14e45fceea467a9c5b2b1a0e1c9d33",      // no hyphens
		"8f14e45f_ceea_467a_9c5b_2b1a0e1c9d33",  // wrong separator
		"8f14e45f-ceea-467a-9c5b-2b1a0e1c9dzz",  // non-hex
		"' OR 1=1 --",
	}
	for _, s := range invalid {
		if IsUUID(s) {
			t.Errorf("IsUUID(%q) = true, want false", s)
		}
	}
}

func TestEmail(t *testing.T) {
	tests := []struct {
		raw     string
		wantErr bool
		want    string
	}{
		{raw: "admin@example.com", want: "admin@example.com"},
		{raw: "  Admin@Example.COM  ", want: "admin@example.com"},
		{raw: "first.last+tag@sub.example.co.uk", want: "first.last+tag@sub.example.co.uk"},
		{raw: "", wantErr: true},
		{raw: "admin", wantErr: true},
		{raw: "admin@", wantErr: true},
		{raw: "@example.com", wantErr: true},
		{raw: "admin@localhost", wantErr: true},
		{raw: "admin@.com", wantErr: true},
		{raw: "admin@example.", wantErr: true},
		{raw: "admin example@x.com", wantErr: true},
		{raw: strings.Repeat("a", 250) + "@example.com", wantErr: true},
	}

	for _, tc := range tests {
		e := New()
		got := Email(e, "email", tc.raw)
		if e.Any() != tc.wantErr {
			t.Errorf("Email(%q): error=%v, want %v (%v)", tc.raw, e.Any(), tc.wantErr, e.Map())
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("Email(%q) = %q, want %q — lowercased for the unique index", tc.raw, got, tc.want)
		}
	}
}

// The behaviour a form depends on: fix all the fields in one round trip.
func TestEveryInvalidFieldIsReportedAtOnce(t *testing.T) {
	e := New()

	RequiredString(e, "title", "ab", 3, 160)
	RequiredText(e, "description", "too short", 20, 20000)
	RequiredString(e, "salary", "", 1, 120)
	RequiredEnum(e, "job_type", "permanent", []string{"full-time", "part-time"})
	ApplyURL(e, "apply_url", "javascript:alert(1)", ApplyURLPolicy{})
	tooLong := strings.Repeat("x", 200)
	OptionalString(e, "country", &tooLong, 80)
	SlugFormat(e, "slug", "Not A Slug")

	details := e.Map()
	want := []string{"title", "description", "salary", "job_type", "apply_url", "country", "slug"}
	if len(details) != len(want) {
		t.Fatalf("details has %d entries, want %d: %v", len(details), len(want), details)
	}
	for _, field := range want {
		if details[field] == "" {
			t.Errorf("details is missing %q: %v", field, details)
		}
	}
}

func TestAddKeepsTheFirstMessagePerField(t *testing.T) {
	e := New()
	e.Add("title", "first")
	e.Add("title", "second")

	if e["title"] != "first" {
		t.Errorf("details[title] = %q, want the first, most specific message", e["title"])
	}
}

func TestEmptyErrorsMapIsNil(t *testing.T) {
	// A `details: {}` in the response would tell a client there are field
	// errors when there are none.
	if got := New().Map(); got != nil {
		t.Errorf("New().Map() = %v, want nil", got)
	}
}

func ptr[T any](v T) *T { return &v }
