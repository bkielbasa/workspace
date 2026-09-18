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
	var rawLines []string
	for scanner.Scan() {
		rawLines = append(rawLines, scanner.Text())
	}

	var unfoldedLines []string
	for _, line := range rawLines {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if len(unfoldedLines) > 0 {
				unfoldedLines[len(unfoldedLines)-1] += line[1:]
			} else {
				unfoldedLines = append(unfoldedLines, line)
			}
		} else {
			unfoldedLines = append(unfoldedLines, line)
		}
	}

	item := &notes.NoteItem{}

	for _, line := range unfoldedLines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(parts[0]))
		val := parts[1]

		switch {
		case key == "UID":
			cleanVal := strings.TrimSpace(val)
			if id, err := uuid.Parse(cleanVal); err == nil {
				item.ID = id
			}
		case key == "SUMMARY":
			item.Content = unescapeText(val)
		case key == "STATUS":
			cleanVal := strings.TrimSpace(val)
			if strings.EqualFold(cleanVal, "COMPLETED") {
				item.Completed = true
			}
		case key == "COMPLETED":
			cleanVal := strings.TrimSpace(val)
			if t, err := time.Parse(icsTimeFormat, cleanVal); err == nil {
				item.CompletedAt = &t
			}
		case key == "CREATED":
			cleanVal := strings.TrimSpace(val)
			if t, err := time.Parse(icsTimeFormat, cleanVal); err == nil {
				item.CreatedAt = t
			} else if t, err := time.Parse(time.RFC3339, cleanVal); err == nil {
				item.CreatedAt = t
			}
		case key == "LAST-MODIFIED":
			cleanVal := strings.TrimSpace(val)
			if t, err := time.Parse(icsTimeFormat, cleanVal); err == nil {
				item.UpdatedAt = t
			} else if t, err := time.Parse(time.RFC3339, cleanVal); err == nil {
				item.UpdatedAt = t
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
	var sb strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			next := runes[i+1]
			switch next {
			case 'n', 'N':
				sb.WriteRune('\n')
				i++
			case ',':
				sb.WriteRune(',')
				i++
			case ';':
				sb.WriteRune(';')
				i++
			case '\\':
				sb.WriteRune('\\')
				i++
			default:
				sb.WriteRune(runes[i])
			}
		} else {
			sb.WriteRune(runes[i])
		}
	}
	return sb.String()
}
