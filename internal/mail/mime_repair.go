package mail

import (
	"regexp"
	"strings"
)

// boundaryParam finds the top-level MIME boundary in a raw message, tolerating
// headers folded across lines (as real-world senders emit them). The s flag
// lets . span the fold; the value stops at the closing quote, semicolon, or
// line end.
var boundaryParam = regexp.MustCompile(`(?ims)^content-type:.*?boundary\s*=\s*"?([^";\r\n]+)`)

// EnsureHeaderBodySeparator repairs stored messages whose blank line between
// the header block and the body went missing (an ingest bug that ate exactly
// one CRLF when rejoining headers and body). Without the separator,
// net/mail treats the first boundary as a header line and the whole message
// becomes unreadable: no body, no snippet, no attachments, no invites.
//
// Only the region before the first boundary delimiter is examined: blank
// lines inside the body must not count. Messages that already separate
// headers from the body, or have no detectable boundary, are untouched.
func EnsureHeaderBodySeparator(raw string) string {
	m := boundaryParam.FindStringSubmatch(raw)
	if len(m) < 2 {
		return raw
	}
	boundary := strings.Trim(strings.TrimSpace(m[1]), `"`)
	if boundary == "" {
		return raw
	}
	marker := "--" + boundary
	idx := strings.Index(raw, "\n"+marker)
	if idx < 0 {
		if strings.HasPrefix(raw, marker) {
			return "\r\n" + raw
		}
		return raw
	}
	// idx points at the newline ending the last header line. A blank line
	// is present exactly when that newline is itself preceded by one.
	if idx >= 2 && raw[idx-1] == '\r' && raw[idx-2] == '\n' {
		return raw
	}
	return raw[:idx+1] + "\r\n" + raw[idx+1:]
}
