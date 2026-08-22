package jobs

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

var randomSlugPattern = regexp.MustCompile(`^job-[0-9a-f]{8}$`)

func TestDeriveSlug(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{
			name:  "the contract's own example",
			title: "Senior Cloud Engineer",
			want:  "senior-cloud-engineer",
		},
		{
			name:  "accents are folded",
			title: "Señor Diseñador Gráfico",
			want:  "senor-disenador-grafico",
		},
		{
			name:  "decomposed accents in a short title",
			title: "Señor Café",
			want:  "senor-cafe",
		},
		{
			name:  "German ligature and umlauts",
			title: "Straßenbau Ingenieür für Köln",
			want:  "strassenbau-ingenieur-fur-koln",
		},
		{
			name:  "runs of punctuation collapse to one hyphen",
			title: "Go / Rust  &&  C++ Engineer!!! (Remote)",
			want:  "go-rust-c-engineer-remote",
		},
		{
			name:  "leading and trailing junk is trimmed",
			title: "   ***  Staff Engineer  ---   ",
			want:  "staff-engineer",
		},
		{
			name:  "an em dash separates rather than vanishing",
			title: "Engineer — Platform",
			want:  "engineer-platform",
		},
		{
			name:  "typographic apostrophe separates",
			title: "O’Brien Consulting Lead",
			want:  "o-brien-consulting-lead",
		},
		{
			name:  "digits survive",
			title: "Tier 3 Support Engineer 2026",
			want:  "tier-3-support-engineer-2026",
		},
		{
			name:  "already a slug",
			title: "senior-cloud-engineer",
			want:  "senior-cloud-engineer",
		},
		{
			name:  "case is folded",
			title: "SENIOR CLOUD ENGINEER",
			want:  "senior-cloud-engineer",
		},
		{
			name:  "emoji is a separator",
			title: "Remote 🚀 Engineer",
			want:  "remote-engineer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveSlug(tc.title); got != tc.want {
				t.Errorf("DeriveSlug(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

// Decomposed input: the titles below are stored as a base letter followed by a
// combining mark (U+0303, U+0308, U+030A, U+0301) rather than a precomposed
// character. This is what a macOS filesystem paste produces.
//
// It is the path the combining-mark pass exists for. Without it the base letter
// would survive the fold table and the mark would fall through to the separator
// branch, turning "Señor" into "sen-or".
//
// If an editor ever re-normalises this file to NFC the assertions still hold,
// because the fold table covers the precomposed forms too — the test stops
// covering the mark-stripping path, which is why this note exists.
func TestDeriveSlugFoldsDecomposedAccents(t *testing.T) {
	tests := map[string]string{
		"Señor Café":           "senor-cafe",
		"Südwest Ingenieur":     "sudwest-ingenieur",
		"Åpenspecialist":        "apenspecialist",
		"Senior Cloud Engineeŕ": "senior-cloud-engineer",
	}
	for title, want := range tests {
		if got := DeriveSlug(title); got != want {
			t.Errorf("DeriveSlug(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestDeriveSlugTruncatesOnAHyphenBoundary(t *testing.T) {
	// 12 words of 8 characters: the untruncated slug is 107 characters. The 80th
	// character is the hyphen that follows the ninth word, so the window already
	// ends on a boundary and all nine whole words must survive — walking back
	// unconditionally would drop one for nothing.
	title := strings.TrimSpace(strings.Repeat("Kubernet ", 12))
	got := DeriveSlug(title)

	if len(got) > MaxSlugLen {
		t.Fatalf("DeriveSlug() = %q (%d chars), want at most %d", got, len(got), MaxSlugLen)
	}
	if strings.HasSuffix(got, "-") || strings.HasPrefix(got, "-") {
		t.Errorf("DeriveSlug() = %q, want no dangling hyphen", got)
	}
	// The cut must land between words, so every segment is a whole "kubernet".
	for _, segment := range strings.Split(got, "-") {
		if segment != "kubernet" {
			t.Errorf("DeriveSlug() = %q; segment %q is a sliced word, not a hyphen-boundary cut", got, segment)
		}
	}
	if want := 9; len(strings.Split(got, "-")) != want {
		t.Errorf("DeriveSlug() = %q with %d segments, want %d whole words inside %d chars",
			got, len(strings.Split(got, "-")), want, MaxSlugLen)
	}
}

func TestDeriveSlugHardCutsAnUnbrokenWord(t *testing.T) {
	// No hyphen to fall back on: a hard cut is the only option, and it must
	// still respect the length cap.
	got := DeriveSlug(strings.Repeat("a", 200))

	if len(got) != MaxSlugLen {
		t.Errorf("DeriveSlug(200 a's) = %q (%d chars), want exactly %d", got, len(got), MaxSlugLen)
	}
}

func TestDeriveSlugFallsBackWhenNothingIsRepresentable(t *testing.T) {
	for _, title := range []string{
		"東京都のエンジニア",   // CJK only
		"Разработчик", // Cyrillic only
		"***",         // punctuation only
		"   ",         // whitespace only
		"",            // empty
	} {
		got := DeriveSlug(title)
		if !randomSlugPattern.MatchString(got) {
			t.Errorf("DeriveSlug(%q) = %q, want the job-<8 hex> fallback", title, got)
		}
	}
}

func TestDeriveSlugFallbackIsUnique(t *testing.T) {
	// Two CJK-titled jobs must not derive the same slug, or the second create
	// would fight the unique index for no reason.
	first := DeriveSlug("東京")
	second := DeriveSlug("東京")
	if first == second {
		t.Errorf("two fallback slugs are identical (%q); the suffix is not random", first)
	}
}

func TestDeriveSlugEscapesReservedWords(t *testing.T) {
	tests := map[string]string{
		"Admin":   "admin-job",
		"admin":   "admin-job",
		"Jobs":    "jobs-job",
		"API":     "api-job",
		"New":     "new-job",
		"Login":   "login-job",
		"Health":  "health-job",
		"Sitemap": "sitemap-job",
		"Robots":  "robots-job",
	}
	for title, want := range tests {
		if got := DeriveSlug(title); got != want {
			t.Errorf("DeriveSlug(%q) = %q, want %q", title, got, want)
		}
	}

	// Only an exact match is reserved. "Admin Assistant" is a real job title.
	if got := DeriveSlug("Admin Assistant"); got != "admin-assistant" {
		t.Errorf("DeriveSlug(\"Admin Assistant\") = %q, want admin-assistant", got)
	}
}

func TestNormaliseSlug(t *testing.T) {
	tests := map[string]string{
		"Senior Cloud Engineer": "senior-cloud-engineer",
		"already-a-slug":        "already-a-slug",
		"UPPER_CASE":            "upper-case",
		"  padded  ":            "padded",
		"a--b":                  "a-b",
		"***":                   "",
	}
	for raw, want := range tests {
		if got := NormaliseSlug(raw); got != want {
			t.Errorf("NormaliseSlug(%q) = %q, want %q", raw, got, want)
		}
	}

	// An explicit slug may be up to 100 characters, unlike a derived one.
	long := NormaliseSlug(strings.Repeat("a", 150))
	if len(long) != 100 {
		t.Errorf("NormaliseSlug(150 a's) = %d chars, want 100", len(long))
	}
}

func TestDeduplicateSlug(t *testing.T) {
	t.Run("free base is returned unchanged", func(t *testing.T) {
		got, err := DeduplicateSlug("senior-cloud-engineer", takenSet())
		if err != nil {
			t.Fatalf("DeduplicateSlug() = %v", err)
		}
		if got != "senior-cloud-engineer" {
			t.Errorf("got %q, want the base unchanged", got)
		}
	})

	t.Run("first collision yields -2", func(t *testing.T) {
		got, err := DeduplicateSlug("senior-cloud-engineer", takenSet("senior-cloud-engineer"))
		if err != nil {
			t.Fatalf("DeduplicateSlug() = %v", err)
		}
		if got != "senior-cloud-engineer-2" {
			t.Errorf("got %q, want senior-cloud-engineer-2", got)
		}
	})

	t.Run("second collision yields -3", func(t *testing.T) {
		got, err := DeduplicateSlug("senior-cloud-engineer",
			takenSet("senior-cloud-engineer", "senior-cloud-engineer-2"))
		if err != nil {
			t.Fatalf("DeduplicateSlug() = %v", err)
		}
		if got != "senior-cloud-engineer-3" {
			t.Errorf("got %q, want senior-cloud-engineer-3", got)
		}
	})

	t.Run("skips a gap rather than reusing a number", func(t *testing.T) {
		// -2 taken, -3 free: the loop must not stop at the first taken suffix.
		got, err := DeduplicateSlug("x", takenSet("x", "x-2"))
		if err != nil {
			t.Fatalf("DeduplicateSlug() = %v", err)
		}
		if got != "x-3" {
			t.Errorf("got %q, want x-3", got)
		}
	})

	t.Run("falls back to a random suffix past the attempt ceiling", func(t *testing.T) {
		// Everything is taken: the loop must terminate rather than query forever.
		got, err := DeduplicateSlug("x", func(string) (bool, error) { return true, nil })
		if err != nil {
			t.Fatalf("DeduplicateSlug() = %v", err)
		}
		if !strings.HasPrefix(got, "x-") || len(got) != len("x-")+8 {
			t.Errorf("got %q, want x-<8 hex>", got)
		}
	})

	t.Run("a lookup failure propagates", func(t *testing.T) {
		sentinel := errors.New("connection refused")
		_, err := DeduplicateSlug("x", func(string) (bool, error) { return false, sentinel })
		if !errors.Is(err, sentinel) {
			t.Errorf("DeduplicateSlug() = %v, want the wrapped lookup error", err)
		}
	})
}

// takenSet builds a SlugTaken over a fixed set of existing slugs.
func takenSet(existing ...string) SlugTaken {
	set := make(map[string]bool, len(existing))
	for _, slug := range existing {
		set[slug] = true
	}
	return func(candidate string) (bool, error) { return set[candidate], nil }
}
