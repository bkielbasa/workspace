package vtodo

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

const icsTimeFormat = "20060102T150405Z"

func Format(item notes.NoteItem) string {
	var sb strings.Builder
	sb.WriteString("BEGIN:VCALENDAR\r\n")
	sb.WriteString("VERSION:2.0\r\n")
	sb.WriteString("PRODID:-//Workspace//Notes VTODO 1.0//EN\r\n")
	sb.WriteString("BEGIN:VTODO\r\n")
	sb.WriteString(fmt.Sprintf("UID:%s\r\n", item.ID.String()))
	sb.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", time.Now().UTC().Format(icsTimeFormat)))
	sb.WriteString(fmt.Sprintf("CREATED:%s\r\n", item.CreatedAt.UTC().Format(icsTimeFormat)))
	sb.WriteString(fmt.Sprintf("LAST-MODIFIED:%s\r\n", item.UpdatedAt.UTC().Format(icsTimeFormat)))
	sb.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", escapeText(item.Content)))

	if item.Completed {
		sb.WriteString("STATUS:COMPLETED\r\n")
		if item.CompletedAt != nil {
			sb.WriteString(fmt.Sprintf("COMPLETED:%s\r\n", item.CompletedAt.UTC().Format(icsTimeFormat)))
		}
	} else {
		sb.WriteString("STATUS:NEEDS-ACTION\r\n")
	}

	sb.WriteString("END:VTODO\r\n")
	sb.WriteString("END:VCALENDAR\r\n")
	return sb.String()
}

func Parse(ics string) (*notes.NoteItem, error) {
	scanner := bufio.NewScanner(strings.NewReader(ics))
	item := &notes.NoteItem{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch {
		case key == "UID":
			if id, err := uuid.Parse(val); err == nil {
				item.ID = id
			}
		case key == "SUMMARY":
			item.Content = unescapeText(val)
		case key == "STATUS":
			if strings.EqualFold(val, "COMPLETED") {
				item.Completed = true
			}
		case key == "COMPLETED":
			if t, err := time.Parse(icsTimeFormat, val); err == nil {
				item.CompletedAt = &t
			}
		}
	}

	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	return item, nil
}

func escapeText(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func unescapeText(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\,", ",")
	s = strings.ReplaceAll(s, "\\;", ";")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}
