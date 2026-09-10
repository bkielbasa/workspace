package web

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"regexp"
	"strings"
	"time"
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
	ID         string
	Sender     string
	SenderName string
	SenderAddr string
	Recipients []string
	Subject    string
	BodyText   string
	Seen       bool
	Flagged    bool
	ReceivedAt time.Time
	BoxName    string
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
		boundary := params["boundary"]
		if boundary != "" {
			mr := multipart.NewReader(bytes.NewReader(bodyBytes), boundary)
			var htmlFallback string
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				pType, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
				pEnc := p.Header.Get("Content-Transfer-Encoding")
				reader := decodeTransfer(p, pEnc)
				data, _ := io.ReadAll(reader)
				if strings.HasPrefix(pType, "text/plain") && textBody == "" {
					textBody = string(data)
				} else if strings.HasPrefix(pType, "text/html") && htmlFallback == "" {
					htmlFallback = string(data)
				}
			}
			if textBody == "" && htmlFallback != "" {
				textBody = stripHTML(htmlFallback)
			}
		}
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

func parseSender(sender string) (name string, addr string) {
	if a, err := netmail.ParseAddress(sender); err == nil {
		if a.Name != "" {
			return a.Name, a.Address
		}
		if parts := strings.Split(a.Address, "@"); len(parts) > 0 && parts[0] != "" {
			return parts[0], a.Address
		}
		return a.Address, a.Address
	}
	return sender, sender
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
