package web

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/mail"
)

type mailViewItem struct {
	ID         string
	Sender     string
	SenderName string
	Recipients []string
	Subject    string
	Snippet    string
	Seen       bool
	Flagged    bool
	ReceivedAt time.Time
}

type mailViewDetail struct {
	ID          string
	Sender      string
	SenderName  string
	SenderAddr  string
	Recipients  []string
	Subject     string
	BodyText    string
	Seen        bool
	Flagged     bool
	ReceivedAt  time.Time
	BoxName     string
	Attachments []mail.AttachmentInfo
	// Invite is set when the message carries a meeting invitation.
	Invite *mailInvite
}

// composeRecipient is one selectable address for the compose To field:
// every filled email of every contact.
type composeRecipient struct {
	Name  string
	Email string
}

// formatBytes renders a byte count for the attachment list.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	res := htmlTagPattern.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(res), " ")
}

func decodeTransfer(r io.Reader, encoding string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	default:
		return r
	}
}

func cleanSnippet(text string, maxLen int) string {
	fields := strings.Fields(text)
	joined := strings.Join(fields, " ")
	if len(joined) <= maxLen {
		return joined
	}
	return joined[:maxLen] + "..."
}

func parseMailContent(raw string, fallbackMime string) (body string, snippet string) {
	if raw == "" {
		return "", ""
	}

	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		b := strings.TrimSpace(raw)
		return b, cleanSnippet(b, 120)
	}

	contentType := msg.Header.Get("Content-Type")
	encoding := msg.Header.Get("Content-Transfer-Encoding")

	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		bodyBytes = []byte(raw)
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = fallbackMime
	}

	var textBody string
	if strings.HasPrefix(mediaType, "multipart/") {
		textBody = walkTextParts(bodyBytes, params["boundary"])
	} else {
		reader := decodeTransfer(bytes.NewReader(bodyBytes), encoding)
		data, _ := io.ReadAll(reader)
		if strings.HasPrefix(mediaType, "text/html") {
			textBody = stripHTML(string(data))
		} else {
			textBody = string(data)
		}
	}

	textBody = strings.TrimSpace(textBody)
	if textBody == "" {
		parts := strings.SplitN(raw, "\r\n\r\n", 2)
		if len(parts) == 2 {
			textBody = strings.TrimSpace(parts[1])
		} else {
			parts = strings.SplitN(raw, "\n\n", 2)
			if len(parts) == 2 {
				textBody = strings.TrimSpace(parts[1])
			} else {
				textBody = strings.TrimSpace(raw)
			}
		}
	}

	snippet = cleanSnippet(textBody, 120)
	return textBody, snippet
}

// walkTextParts extracts the readable body from (possibly nested) multiparts,
// preferring the first text/plain part at any depth and falling back to
// stripped text/html. Real-world invites (e.g. Apple) nest alternative
// inside mixed, which a single-level walk misses entirely.
func walkTextParts(body []byte, boundary string) string {
	if boundary == "" {
		return ""
	}
	var plain, html string
	var walk func(data []byte, bound string)
	walk = func(data []byte, bound string) {
		if bound == "" {
			return
		}
		mr := multipart.NewReader(bytes.NewReader(data), bound)
		for {
			p, err := mr.NextPart()
			if err != nil {
				return
			}
			pType, pParams, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			raw, err := io.ReadAll(p)
			if err != nil {
				continue
			}
			if strings.HasPrefix(pType, "multipart/") {
				walk(raw, pParams["boundary"])
				continue
			}
			text, _ := io.ReadAll(decodeTransfer(bytes.NewReader(raw), p.Header.Get("Content-Transfer-Encoding")))
			switch {
			case strings.HasPrefix(pType, "text/plain") && plain == "":
				plain = string(text)
			case strings.HasPrefix(pType, "text/html") && html == "":
				html = string(text)
			}
		}
	}
	walk(body, boundary)
	if plain != "" {
		return plain
	}
	return stripHTML(html)
}

func parseSender(sender string) (name string, addr string) {
	if a, err := netmail.ParseAddress(sender); err == nil {
		if a.Name != "" {
			return decodeHeader(a.Name), a.Address
		}
		if parts := strings.Split(a.Address, "@"); len(parts) > 0 && parts[0] != "" {
			return parts[0], a.Address
		}
		return a.Address, a.Address
	}
	return decodeHeader(sender), sender
}

// decodeHeader decodes RFC 2047 encoded words (=?charset?Q?...?=) found in
// Subject/From headers. Non-UTF8 charsets fall back to the raw value when
// no charset reader is available.
func decodeHeader(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	dec := new(mime.WordDecoder)
	if decoded, err := dec.DecodeHeader(s); err == nil {
		return decoded
	}
	return s
}

func mailboxIcon(name string) string {
	switch strings.ToUpper(name) {
	case "INBOX":
		return "📥"
	case "SENT":
		return "📤"
	case "DRAFTS":
		return "📝"
	case "TRASH":
		return "🗑"
	case "ARCHIVE":
		return "📁"
	case "SPAM":
		return "⚠️"
	case "IMPORTANT":
		return "🏷"
	case "ALL":
		return "🗂"
	default:
		return "📁"
	}
}

func mailboxTitle(name string) string {
	if strings.EqualFold(name, "INBOX") {
		return "Inbox"
	}
	return name
}

func formatMailDate(t time.Time) string {
	now := time.Now().Local()
	locT := t.Local()
	if locT.Year() == now.Year() && locT.YearDay() == now.YearDay() {
		return locT.Format("15:04")
	}
	if locT.Year() == now.Year() {
		return locT.Format("Jan 2")
	}
	return locT.Format("Jan 2, 2006")
}

func formatDetailDate(t time.Time) string {
	return t.Local().Format("Jan 2, 2006, 15:04")
}

func contactInitialFromSender(sender string) string {
	name, addr := parseSender(sender)
	target := name
	if target == "" {
		target = addr
	}
	for _, r := range target {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return strings.ToUpper(string(r))
		}
	}
	return "M"
}
