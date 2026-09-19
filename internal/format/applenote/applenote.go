package applenote

import (
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type ParsedNote struct {
	ID        uuid.UUID
	Title     string
	Body      string
	IsNote    bool
	Date      time.Time
}

func Format(note *notes.Note, userEmail string) string {
	dateStr := note.UpdatedAt.Format(time.RFC1123Z)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s\r\n", userEmail))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", note.Title))
	sb.WriteString(fmt.Sprintf("Date: %s\r\n", dateStr))
	sb.WriteString(fmt.Sprintf("Message-ID: <%s@workspace.local>\r\n", note.ID))
	sb.WriteString("X-Uniform-Type-Identifier: com.apple.mail-note\r\n")
	sb.WriteString(fmt.Sprintf("X-Universally-Unique-Identifier: %s\r\n", note.ID))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	sb.WriteString(note.Body)
	return sb.String()
}

var (
	htmlDivBreakRe = regexp.MustCompile(`(?i)<(div|p|br)[^>]*>`)
	htmlTagStripRe = regexp.MustCompile(`<[^>]+>`)
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

	date := time.Now()
	if dateHeader := headers.Get("Date"); dateHeader != "" {
		if parsedTime, err := mail.ParseDate(dateHeader); err == nil {
			date = parsedTime
		}
	}

	bodyBytes, _ := io.ReadAll(msg.Body)
	rawBody := string(bodyBytes)

	contentType := strings.ToLower(headers.Get("Content-Type"))
	body := rawBody
	if strings.Contains(contentType, "text/html") {
		// Convert HTML line breaks / divs to newlines and strip remaining tags
		withNewlines := htmlDivBreakRe.ReplaceAllString(rawBody, "\n")
		stripped := htmlTagStripRe.ReplaceAllString(withNewlines, "")
		body = strings.TrimSpace(stripped)
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
		body = strings.TrimSpace(rawBody)
	}

	return &ParsedNote{
		ID:     noteID,
		Title:  subject,
		Body:   body,
		IsNote: isNote,
		Date:   date,
	}, nil
}
