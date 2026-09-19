package applenote

import (
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Note struct {
	ID        uuid.UUID
	Title     string
	Body      string
	UpdatedAt time.Time
}

type FormattableNote interface {
	AppleNoteFields() (id uuid.UUID, title, body string, updatedAt time.Time)
}

func (n *Note) AppleNoteFields() (uuid.UUID, string, string, time.Time) {
	return n.ID, n.Title, n.Body, n.UpdatedAt
}

type ParsedNote struct {
	ID        uuid.UUID
	Title     string
	Body      string
	IsNote    bool
	Date      time.Time
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func Format(note any, userEmail string) string {
	var (
		id        uuid.UUID
		title     string
		body      string
		updatedAt time.Time
	)

	if fn, ok := note.(FormattableNote); ok {
		id, title, body, updatedAt = fn.AppleNoteFields()
	} else if n, ok := note.(*Note); ok && n != nil {
		id, title, body, updatedAt = n.ID, n.Title, n.Body, n.UpdatedAt
	} else if n, ok := note.(Note); ok {
		id, title, body, updatedAt = n.ID, n.Title, n.Body, n.UpdatedAt
	} else if note != nil {
		v := reflect.ValueOf(note)
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() == reflect.Struct {
			if f := v.FieldByName("ID"); f.IsValid() {
				if uid, ok := f.Interface().(uuid.UUID); ok {
					id = uid
				}
			}
			if f := v.FieldByName("Title"); f.IsValid() {
				title = f.String()
			}
			if f := v.FieldByName("Body"); f.IsValid() {
				body = f.String()
			}
			if f := v.FieldByName("UpdatedAt"); f.IsValid() {
				if t, ok := f.Interface().(time.Time); ok {
					updatedAt = t
				}
			}
		}
	}

	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	dateStr := updatedAt.Format(time.RFC1123Z)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s\r\n", userEmail))

	if !isASCII(title) {
		title = mime.QEncoding.Encode("utf-8", title)
	}
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", title))
	sb.WriteString(fmt.Sprintf("Date: %s\r\n", dateStr))
	sb.WriteString(fmt.Sprintf("Message-ID: <%s@workspace.local>\r\n", id))
	sb.WriteString("X-Uniform-Type-Identifier: com.apple.mail-note\r\n")
	sb.WriteString(fmt.Sprintf("X-Universally-Unique-Identifier: %s\r\n", id))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	sb.WriteString(body)
	return sb.String()
}

var (
	htmlDivBreakRe = regexp.MustCompile(`(?i)<(div|p|br)[^>]*>`)
	htmlTagStripRe = regexp.MustCompile(`<[^>]+>`)
	htmlStyleRe    = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlScriptRe   = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
)

func Parse(raw string) (*ParsedNote, error) {
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("read mime message: %w", err)
	}

	headers := msg.Header
	uniformType := headers.Get("X-Uniform-Type-Identifier")
	isNote := strings.EqualFold(uniformType, "com.apple.mail-note")

	rawUUID := headers.Get("X-Universally-Unique-Identifier")
	noteID, _ := uuid.Parse(rawUUID)

	subject := headers.Get("Subject")
	if decoded, err := (&mime.WordDecoder{}).DecodeHeader(subject); err == nil {
		subject = decoded
	}

	date := time.Now()
	if dateHeader := headers.Get("Date"); dateHeader != "" {
		if parsedTime, err := mail.ParseDate(dateHeader); err == nil {
			date = parsedTime
		}
	}

	transferEncoding := strings.ToLower(strings.TrimSpace(headers.Get("Content-Transfer-Encoding")))
	var bodyReader io.Reader = msg.Body
	switch transferEncoding {
	case "quoted-printable":
		bodyReader = quotedprintable.NewReader(msg.Body)
	case "base64":
		bodyReader = base64.NewDecoder(base64.StdEncoding, msg.Body)
	}

	bodyBytes, _ := io.ReadAll(bodyReader)
	rawBody := string(bodyBytes)

	contentType := strings.ToLower(headers.Get("Content-Type"))
	body := rawBody
	if strings.Contains(contentType, "text/html") {
		// Strip style and script blocks first
		htmlCleaned := htmlStyleRe.ReplaceAllString(rawBody, "")
		htmlCleaned = htmlScriptRe.ReplaceAllString(htmlCleaned, "")

		// Convert HTML line breaks / divs to newlines and strip remaining tags
		withNewlines := htmlDivBreakRe.ReplaceAllString(htmlCleaned, "\n")
		stripped := htmlTagStripRe.ReplaceAllString(withNewlines, "")
		body = strings.TrimSpace(html.UnescapeString(stripped))
		// If subject was empty or default, first line can serve as title
		if subject == "" {
			lines := strings.SplitN(body, "\n", 2)
			if len(lines) > 0 {
				subject = strings.TrimSpace(lines[0])
			}
		}
		// If first line of extracted body matches the subject, remove it from body
		if lines := strings.SplitN(body, "\n", 2); len(lines) > 1 && strings.TrimSpace(lines[0]) == subject {
			body = strings.TrimSpace(lines[1])
		}
	} else {
		body = rawBody
	}

	return &ParsedNote{
		ID:     noteID,
		Title:  subject,
		Body:   body,
		IsNote: isNote,
		Date:   date,
	}, nil
}
