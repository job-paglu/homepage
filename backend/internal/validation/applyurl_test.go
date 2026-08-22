package validation

import (
	"strings"
	"testing"
)

var (
	strict     = ApplyURLPolicy{}
	httpOK     = ApplyURLPolicy{AllowHTTP: true}
	developing = ApplyURLPolicy{AllowHTTP: true, AllowLocalhost: true}
)

func TestValidateApplyURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		policy  ApplyURLPolicy
		wantErr bool
		wantMsg string
	}{
		// -------------------------------------------------- dangerous schemes
		{name: "javascript", raw: "javascript:alert(1)", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "javascript mixed case", raw: "JavaScript:alert(1)", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "javascript upper case", raw: "JAVASCRIPT:alert(1)", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "data html", raw: "data:text/html,<script>alert(1)</script>", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "data base64", raw: "data:text/html;base64,PHNjcmlwdD4=", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "vbscript", raw: "vbscript:msgbox(1)", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "file", raw: "file:///etc/passwd", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "blob", raw: "blob:https://example.com/1234-5678", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "about", raw: "about:blank", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "mailto", raw: "mailto:jobs@example.com", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "tel", raw: "tel:+15551234567", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "ftp", raw: "ftp://example.com/jobs", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},

		// ------------------------------------------------------- not absolute
		{name: "scheme relative", raw: "//evil.com/apply", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "root relative", raw: "/relative/path", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "bare host", raw: "example.com/jobs", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "https with no authority", raw: "https:example.com", policy: strict, wantErr: true, wantMsg: msgApplyURLHost},
		{name: "https with empty host", raw: "https:///jobs", policy: strict, wantErr: true, wantMsg: msgApplyURLHost},

		// ---------------------------------------------------------- userinfo
		{name: "user and password", raw: "https://user:pass@host.example/x", policy: strict, wantErr: true, wantMsg: msgApplyURLUserinfo},
		{name: "user only", raw: "https://user@host.example/x", policy: strict, wantErr: true, wantMsg: msgApplyURLUserinfo},

		// ------------------------------------------ whitespace and controls
		{name: "embedded newline", raw: "https://example.com/a\nb", policy: strict, wantErr: true, wantMsg: msgApplyURLWhitespace},
		{name: "embedded carriage return", raw: "https://example.com/a\rb", policy: strict, wantErr: true, wantMsg: msgApplyURLWhitespace},
		{name: "embedded tab", raw: "https://example.com/a\tb", policy: strict, wantErr: true, wantMsg: msgApplyURLWhitespace},
		{name: "leading space before scheme", raw: "  javascript:alert(1)", policy: strict, wantErr: true, wantMsg: msgApplyURLWhitespace},
		{name: "internal space", raw: "https://example.com/a b", policy: strict, wantErr: true, wantMsg: msgApplyURLWhitespace},
		{name: "null byte", raw: "https://example.com/a\x00b", policy: strict, wantErr: true, wantMsg: msgApplyURLWhitespace},

		// ------------------------------------------------------------ length
		{
			name:    "over 2048 characters",
			raw:     "https://example.com/" + strings.Repeat("a", 2048),
			policy:  strict,
			wantErr: true,
			wantMsg: msgApplyURLTooLong,
		},
		{
			name:   "exactly 2048 characters",
			raw:    "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/")),
			policy: strict,
		},

		// ------------------------------------------------------------- empty
		{name: "empty", raw: "", policy: strict, wantErr: true, wantMsg: msgApplyURLRequired},
		{name: "whitespace only", raw: "   ", policy: strict, wantErr: true, wantMsg: msgApplyURLRequired},

		// --------------------------------------------------- the http switch
		{name: "http rejected by default", raw: "http://x.com", policy: strict, wantErr: true, wantMsg: msgApplyURLScheme},
		{name: "http accepted when enabled", raw: "http://x.com", policy: httpOK},

		// ---------------------------------------------------------- accepted
		{name: "plain https", raw: "https://x.com", policy: strict},
		{name: "https with path and query", raw: "https://boards.example.com/careers/apply?id=42&src=jobpaglu", policy: strict},
		{name: "https with port", raw: "https://jobs.example.com:8443/apply", policy: strict},
		{name: "https with fragment", raw: "https://example.com/careers#apply", policy: strict},
		{name: "internationalised host", raw: "https://xn--bcher-kva.example/stelle", policy: strict},
		{name: "percent encoded path", raw: "https://example.com/apply/senior%20engineer", policy: strict},

		// ------------------------------------------------------- development
		{name: "localhost in development", raw: "https://localhost/x", policy: developing},
		{name: "localhost with port in development", raw: "http://localhost:3000/apply", policy: developing},
		{name: "localhost rejected in production", raw: "https://localhost/x", policy: strict, wantErr: true, wantMsg: msgApplyURLHost},
		{name: "dotless intranet host rejected", raw: "https://intranet/apply", policy: developing, wantErr: true, wantMsg: msgApplyURLHost},

		// ------------------------------------------------- malformed hosts
		{name: "leading dot host", raw: "https://.example.com/x", policy: strict, wantErr: true, wantMsg: msgApplyURLHost},
		{name: "trailing dot host", raw: "https://example.com./x", policy: strict, wantErr: true, wantMsg: msgApplyURLHost},
		{name: "double dot host", raw: "https://example..com/x", policy: strict, wantErr: true, wantMsg: msgApplyURLHost},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateApplyURL(tc.raw, tc.policy)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateApplyURL(%q) = nil, want a rejection", tc.raw)
				}
				if tc.wantMsg != "" && err.Error() != tc.wantMsg {
					t.Errorf("message = %q, want %q", err.Error(), tc.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateApplyURL(%q) = %v, want nil", tc.raw, err)
			}
		})
	}
}

// The allowlist has to hold for schemes nobody has thought of, which is the
// reason it is an allowlist. This fails if the implementation ever grows a
// denylist branch.
func TestValidateApplyURLRejectsUnknownSchemes(t *testing.T) {
	for _, scheme := range []string{"chrome", "ms-msdt", "steam", "intent", "jar", "view-source", "ws", "wss", "gopher", "x-made-up"} {
		raw := scheme + "://example.com/apply"
		if err := ValidateApplyURL(raw, developing); err == nil {
			t.Errorf("ValidateApplyURL(%q) = nil; the check must be an allowlist, not a denylist", raw)
		}
	}
}

func TestApplyURLRecordsIntoErrorsAndReturnsInputUnchanged(t *testing.T) {
	e := New()
	const bad = "javascript:alert(1)"

	got := ApplyURL(e, "apply_url", bad, strict)

	if got != bad {
		t.Errorf("ApplyURL() = %q, want the input byte-for-byte (contract section 7)", got)
	}
	if e["apply_url"] != msgApplyURLScheme {
		t.Errorf("details[apply_url] = %q, want %q", e["apply_url"], msgApplyURLScheme)
	}

	// A good value must not be normalised on the way through — the stored value
	// is returned to visitors exactly as the admin typed it.
	const messy = "https://Example.COM/Apply?b=2&a=1"
	clean := New()
	if got := ApplyURL(clean, "apply_url", messy, strict); got != messy {
		t.Errorf("ApplyURL() = %q, want %q unchanged", got, messy)
	}
	if clean.Any() {
		t.Errorf("unexpected errors: %v", clean.Map())
	}
}
