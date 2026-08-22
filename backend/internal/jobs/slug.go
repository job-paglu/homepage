package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// MaxSlugLen is the truncation point for a derived slug (contract section 8).
// An explicitly supplied slug may be up to 100 characters; a derived one is kept
// shorter so the de-duplication suffix always fits.
const MaxSlugLen = 80

// ReservedSlugs are paths the frontend routes itself. A job that claimed one
// would be unreachable, or would shadow a real page.
//
// A derived slug that lands on one gets "-job" appended (contract section 8).
var ReservedSlugs = map[string]bool{
	"new": true, "admin": true, "api": true, "jobs": true,
	"login": true, "logout": true, "health": true, "ready": true,
	"sitemap": true, "robots": true,
}

// DeriveSlug turns a title into a URL slug.
//
// Steps, in the contract's order: fold accents, lowercase, collapse every run of
// non-alphanumerics to one hyphen, trim, truncate on a hyphen boundary, then
// escape a reserved word. A title with nothing alphanumeric in it — a CJK-only
// title, or "***" — yields a random `job-xxxxxxxx` instead, because an empty
// slug is not a URL.
func DeriveSlug(title string) string {
	slug := normaliseSlug(title)
	if slug == "" {
		return randomSlug()
	}
	slug = truncateSlug(slug, MaxSlugLen)
	if ReservedSlugs[slug] {
		// Appending rather than replacing keeps the slug recognisable: a job
		// titled "Admin" becomes "admin-job", not "job-3f2a91bc".
		slug += "-job"
	}
	return slug
}

// NormaliseSlug applies the same transformation to an admin-supplied slug.
//
// The contract requires this: "When supplied it is normalised the same way",
// so an admin who types "Senior Cloud Engineer" into the slug field gets a
// working slug rather than a validation error. It returns "" when nothing
// usable survives, which the caller reports as a format failure.
func NormaliseSlug(raw string) string {
	return truncateSlug(normaliseSlug(raw), 100)
}

// normaliseSlug is steps 1 to 4: fold, lowercase, collapse, trim.
func normaliseSlug(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingHyphen := false

	for _, r := range foldAccents(strings.ToLower(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
		default:
			// Every run of anything else — spaces, punctuation, emoji, a CJK
			// character with no Latin equivalent — collapses to one hyphen, and
			// a trailing run produces nothing because the hyphen is only
			// written when a following alphanumeric arrives.
			pendingHyphen = true
		}
	}
	return b.String()
}

// truncateSlug cuts to max characters, preferring a hyphen boundary.
//
// A slug cut mid-word reads as a typo, so the cut walks back to the last hyphen
// inside the window. When the window has no hyphen — one very long word — a hard
// cut is the only option.
func truncateSlug(slug string, max int) string {
	if slug == "" {
		return ""
	}
	runes := []rune(slug)
	if len(runes) <= max {
		return slug
	}

	window := string(runes[:max])
	// If the character that fell off the end was the separator itself, the window
	// already ends on a word boundary and walking back would throw away a whole
	// word for nothing.
	if runes[max] != '-' {
		if idx := strings.LastIndexByte(window, '-'); idx > 0 {
			window = window[:idx]
		}
	}
	return strings.Trim(window, "-")
}

// randomSlug is the fallback for a title with no representable characters.
func randomSlug() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Unreachable in practice. A predictable slug is not a security problem
		// — it is public by definition — so a fixed value beats failing a create.
		return "job-00000000"
	}
	return "job-" + hex.EncodeToString(buf[:])
}

// SlugTaken reports whether a candidate slug already exists among non-deleted
// jobs. Implemented by the repository; declared here because de-duplication is a
// rule, not a query.
type SlugTaken func(candidate string) (bool, error)

// DeduplicateSlug returns base, or base-2, base-3, ... — the first candidate
// that is free (contract section 8).
//
// The loop has a ceiling because it is talking to a database: a bug that made
// `taken` always true would otherwise be an unbounded query storm. Hitting the
// ceiling falls back to a random suffix, which cannot collide in practice.
func DeduplicateSlug(base string, taken SlugTaken) (string, error) {
	const maxAttempts = 50

	used, err := taken(base)
	if err != nil {
		return "", fmt.Errorf("checking slug %q: %w", base, err)
	}
	if !used {
		return base, nil
	}

	for suffix := 2; suffix <= maxAttempts; suffix++ {
		candidate := base + "-" + strconv.Itoa(suffix)
		used, err := taken(candidate)
		if err != nil {
			return "", fmt.Errorf("checking slug %q: %w", candidate, err)
		}
		if !used {
			return candidate, nil
		}
	}

	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generating a fallback slug suffix: %w", err)
	}
	return base + "-" + hex.EncodeToString(buf[:]), nil
}

// --------------------------------------------------------------- accent folding

// foldAccents maps accented Latin characters to their unaccented equivalents and
// drops Unicode combining marks.
//
// Deliberate divergence from contract section 8, which specifies NFKD
// normalisation: golang.org/x/text is the package that provides it, and this
// module is capped at two direct dependencies (pgx and x/crypto). So instead of
// a general normaliser this is an explicit table for the Latin-1 Supplement and
// Latin Extended-A ranges, plus a pass that discards combining marks so
// already-decomposed input ("Sen" + U+0303 + "or") folds too.
//
// The observable difference: a character outside the table is treated as a
// separator rather than transliterated. "Señor" and "Café" behave exactly as the
// contract describes; a Cyrillic or Greek title falls back to `job-xxxxxxxx`
// instead of being romanised, which is the same outcome the contract already
// specifies for a CJK title.
func foldAccents(s string) string {
	// Fast path: pure ASCII, which most titles are.
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case isCombiningMark(r):
			// Drop it: the base letter was already written.
		default:
			if folded, ok := accentFolds[r]; ok {
				b.WriteString(folded)
			} else {
				// Not representable. Emitting a space makes it a separator,
				// which normaliseSlug then collapses to a hyphen.
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}

// isCombiningMark covers the combining diacritical marks block and its
// supplement — the marks that NFKD would have separated out.
func isCombiningMark(r rune) bool {
	return (r >= 0x0300 && r <= 0x036F) || (r >= 0x1AB0 && r <= 0x1AFF) ||
		(r >= 0x1DC0 && r <= 0x1DFF) || (r >= 0x20D0 && r <= 0x20F0) ||
		(r >= 0xFE20 && r <= 0xFE2F)
}

// accentFolds is keyed on lowercase runes only: normaliseSlug lowercases before
// folding, so the uppercase forms never reach here.
var accentFolds = map[rune]string{
	// Latin-1 Supplement
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a",
	'æ': "ae", 'ç': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ð': "d", 'ñ': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u",
	'ý': "y", 'þ': "th", 'ÿ': "y",
	'ß': "ss",

	// Latin Extended-A
	'ā': "a", 'ă': "a", 'ą': "a",
	'ć': "c", 'ĉ': "c", 'ċ': "c", 'č': "c",
	'ď': "d", 'đ': "d",
	'ē': "e", 'ĕ': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ĝ': "g", 'ğ': "g", 'ġ': "g", 'ģ': "g",
	'ĥ': "h", 'ħ': "h",
	'ĩ': "i", 'ī': "i", 'ĭ': "i", 'į': "i", 'ı': "i", 'ĳ': "ij",
	'ĵ': "j", 'ķ': "k",
	'ĺ': "l", 'ļ': "l", 'ľ': "l", 'ŀ': "l", 'ł': "l",
	'ń': "n", 'ņ': "n", 'ň': "n", 'ŉ': "n", 'ŋ': "ng",
	'ō': "o", 'ŏ': "o", 'ő': "o", 'œ': "oe",
	'ŕ': "r", 'ŗ': "r", 'ř': "r",
	'ś': "s", 'ŝ': "s", 'ş': "s", 'š': "s",
	'ţ': "t", 'ť': "t", 'ŧ': "t",
	'ũ': "u", 'ū': "u", 'ŭ': "u", 'ů': "u", 'ű': "u", 'ų': "u",
	'ŵ': "w", 'ŷ': "y", 'ź': "z", 'ż': "z", 'ž': "z", 'ſ': "s",

	// Punctuation that appears in pasted titles and should separate words
	// rather than vanish. Explicit because the default branch already produces a
	// space; listed so the intent is not mistaken for an omission.
	'–': " ", '—': " ", '‑': " ", '·': " ",
	'“': " ", '”': " ", '‘': " ", '’': " ", '…': " ",
}
