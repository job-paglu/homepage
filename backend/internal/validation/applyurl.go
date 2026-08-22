package validation

import (
	"net/url"
	"strings"
)

// MaxApplyURLLen is the cap from contract section 7.
const MaxApplyURLLen = 2048

// ApplyURLPolicy is the environment-dependent part of the check.
//
// Both relaxations exist for local development only and both default to off, so
// a production process that forgets to set them is the strict one.
type ApplyURLPolicy struct {
	// AllowHTTP mirrors ALLOW_HTTP_APPLY_URLS.
	AllowHTTP bool
	// AllowLocalhost permits a hostname with no dot in it, so a developer can
	// point an apply link at their own machine.
	AllowLocalhost bool
}

// The messages a client sees. Kept as constants because they are asserted in
// tests and rendered next to a form field, and because a message that varies by
// reason is a hint about the parser rather than help for the user.
const (
	msgApplyURLRequired   = "This field is required."
	msgApplyURLTooLong    = "Must be at most 2048 characters."
	msgApplyURLWhitespace = "Must not contain spaces, tabs or line breaks."
	msgApplyURLScheme     = "Must be an absolute https:// URL."
	msgApplyURLUserinfo   = "Must not contain a username or password."
	msgApplyURLHost       = "Must include a valid host name, for example example.com."
)

// ApplyURLError is a validation failure with the message to show the user.
type ApplyURLError struct {
	Message string
}

func (e *ApplyURLError) Error() string { return e.Message }

// ValidateApplyURL is the allowlist from contract section 7.
//
// This is the one field where a bad stored value becomes an attack on a visitor
// rather than a cosmetic defect: `javascript:` in an href executes in the
// visitor's session on the frontend's origin. So the check is an allowlist of
// two schemes and everything else is refused, rather than a denylist of the
// dangerous ones — a denylist is a promise to have thought of every scheme, and
// nobody can keep it.
//
// The value is never rewritten. A URL that passes is stored byte-for-byte
// (contract section 7), so normalising it here would break that guarantee.
func ValidateApplyURL(raw string, policy ApplyURLPolicy) error {
	if strings.TrimSpace(raw) == "" {
		return &ApplyURLError{Message: msgApplyURLRequired}
	}

	// Length before parsing: url.Parse on a megabyte of text is wasted work.
	// Bytes, not runes — the cap protects a column and a header, both of which
	// count bytes.
	if len(raw) > MaxApplyURLLen {
		return &ApplyURLError{Message: msgApplyURLTooLong}
	}

	// Rejected before parsing, not trimmed, for two reasons. A leading space
	// turns `  javascript:...` into something some HTML parsers still execute,
	// and an embedded newline is a header/log injection primitive. Refusing is
	// unambiguous; cleaning would mean storing something the admin did not type.
	if strings.ContainsAny(raw, " \t\r\n\v\f") {
		return &ApplyURLError{Message: msgApplyURLWhitespace}
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c < 0x20 || c == 0x7f {
			return &ApplyURLError{Message: msgApplyURLWhitespace}
		}
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return &ApplyURLError{Message: msgApplyURLScheme}
	}

	// url.Parse lowercases the scheme, so `JavaScript:` and `javascript:` land
	// in the same branch. This is the allowlist.
	switch parsed.Scheme {
	case "https":
		// always fine
	case "http":
		if !policy.AllowHTTP {
			return &ApplyURLError{Message: msgApplyURLScheme}
		}
	default:
		// Covers the empty scheme too, which is what `//evil.com` and
		// `/relative/path` produce, and every opaque scheme: javascript, data,
		// vbscript, file, blob, about, mailto, tel and anything invented later.
		return &ApplyURLError{Message: msgApplyURLScheme}
	}

	// `https:` with no authority parses with an empty Host and a path. It is not
	// a usable link and must not reach an href.
	if !parsed.IsAbs() || parsed.Host == "" {
		return &ApplyURLError{Message: msgApplyURLHost}
	}

	// https://user:pass@host is rejected outright. Some browsers still show the
	// userinfo part as though it were the hostname, which makes it a phishing
	// primitive, and an admin has no legitimate reason to embed credentials in a
	// public apply link.
	if parsed.User != nil {
		return &ApplyURLError{Message: msgApplyURLUserinfo}
	}

	host := parsed.Hostname() // strips the port and IPv6 brackets
	if host == "" {
		return &ApplyURLError{Message: msgApplyURLHost}
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return &ApplyURLError{Message: msgApplyURLHost}
	}
	if !strings.Contains(host, ".") {
		// A dotless host is either a bare intranet name, which cannot resolve
		// for a public visitor, or localhost — allowed only in development.
		isDevLocalhost := policy.AllowLocalhost && strings.EqualFold(host, "localhost")
		if !isDevLocalhost {
			return &ApplyURLError{Message: msgApplyURLHost}
		}
	}

	return nil
}

// ApplyURL validates the field and records the failure in e. Returns the value
// unchanged so the caller stores exactly what was checked.
func ApplyURL(e Errors, field, raw string, policy ApplyURLPolicy) string {
	if err := ValidateApplyURL(raw, policy); err != nil {
		e.Add(field, err.Error())
	}
	return raw
}
