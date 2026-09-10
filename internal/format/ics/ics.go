package ics

import (
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/google/uuid"
)

// Parse pulls a best-effort title/start/end/UID out of an iCalendar body for
// the web calendar's columns. The raw body is stored on the event so CalDAV
// can serve it back unchanged. Unknown or timezone-qualified times simply
// yield a zero time. Parse only fails when FromPayload rejects the identity
// (missing user or resource).
func Parse(raw string, userID uuid.UUID, resource string) (calendar.Event, error) {
	var title, uid, location, description string
	var start, end time.Time
	for _, line := range unfold(raw) {
		name, params, value := splitLine(line)
		switch strings.ToUpper(name) {
		case "SUMMARY":
			title = unescapeText(value)
		case "LOCATION":
			location = unescapeText(value)
		case "DESCRIPTION":
			description = unescapeText(value)
		case "UID":
			uid = strings.TrimSpace(value)
		case "DTSTART":
			start = parseTime(params, value)
		case "DTEND":
			end = parseTime(params, value)
		}
	}
	return calendar.FromPayload(userID, calendar.Payload{
		Resource:    resource,
		ICS:         raw,
		UID:         uid,
		Title:       title,
		Location:    location,
		Description: description,
		StartsAt:    start,
		EndsAt:      end,
	})
}

// Encode serializes an event as a full iCalendar object. Clients (iOS, DAVx5)
// drop VEVENTs that have no UID or DTSTAMP, and require the envelope to be
// versioned, so the minimal object below is enough to be importable.
func Encode(e calendar.Event) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\n")
	b.WriteString("VERSION:2.0\n")
	b.WriteString("PRODID:-//Workspace//Workspace//EN\n")
	b.WriteString("CALSCALE:GREGORIAN\n")
	b.WriteString("BEGIN:VEVENT\n")
	b.WriteString("UID:" + e.ID.String() + "\n")
	b.WriteString("DTSTAMP:" + e.UpdatedAt.UTC().Format("20060102T150405Z") + "\n")
	b.WriteString("DTSTART:" + e.StartsAt.UTC().Format("20060102T150405Z") + "\n")
	b.WriteString("DTEND:" + e.EndsAt.UTC().Format("20060102T150405Z") + "\n")
	b.WriteString("SUMMARY:" + escapeText(e.Title) + "\n")
	if e.Location != "" {
		b.WriteString("LOCATION:" + escapeText(e.Location) + "\n")
	}
	if e.Description != "" {
		b.WriteString("DESCRIPTION:" + escapeText(e.Description) + "\n")
	}
	b.WriteString("END:VEVENT\n")
	b.WriteString("END:VCALENDAR\n")
	return b.String()
}

// unfold splits an iCalendar object into logical lines, joining RFC 5545
// folded continuations (a line starting with a space or tab) and trimming CRLF.
func unfold(raw string) []string {
	var lines []string
	var buf string
	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		if line != "" && (line[0] == ' ' || line[0] == '\t') && buf != "" {
			buf += line[1:]
			continue
		}
		if buf != "" {
			lines = append(lines, buf)
		}
		buf = line
	}
	if buf != "" {
		lines = append(lines, buf)
	}
	return lines
}

// splitLine breaks "NAME;PARAM=v:VALUE" into its parts.
func splitLine(line string) (name, params, value string) {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return line, "", ""
	}
	head, value := line[:colon], line[colon+1:]
	if semi := strings.IndexByte(head, ';'); semi >= 0 {
		return head[:semi], head[semi+1:], value
	}
	return head, "", value
}

// parseTime handles the common DTSTART/DTEND forms: UTC (Z), floating local
// date-time, and all-day dates (VALUE=DATE). Timezone-qualified values are read
// as their wall-clock time; exact zone math is unnecessary because the raw ICS
// is served back unchanged.
func parseTime(params, value string) time.Time {
	value = strings.TrimSpace(value)
	if strings.Contains(strings.ToUpper(params), "VALUE=DATE") || len(value) == 8 {
		if t, err := time.Parse("20060102", value); err == nil {
			return t
		}
	}
	for _, layout := range []string{"20060102T150405Z", "20060102T150405"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func unescapeText(v string) string {
	v = strings.ReplaceAll(v, `\n`, "\n")
	v = strings.ReplaceAll(v, `\N`, "\n")
	v = strings.ReplaceAll(v, `\,`, ",")
	v = strings.ReplaceAll(v, `\;`, ";")
	v = strings.ReplaceAll(v, `\\`, `\`)
	return strings.TrimSpace(v)
}

func escapeText(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	v = strings.ReplaceAll(v, ",", `\,`)
	v = strings.ReplaceAll(v, ";", `\;`)
	return v
}
