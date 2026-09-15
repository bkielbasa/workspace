package ics

import (
	"fmt"
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
	payload, err := ParsePayload(raw)
	if err != nil {
		return calendar.Event{}, err
	}
	payload.Resource = resource
	return calendar.FromPayload(userID, payload)
}

// ParsePayload extracts event and scheduling fields without identity
// validation, for callers (mail invites) that build the event themselves.
func ParsePayload(raw string) (calendar.Payload, error) {
	var title, uid, location, description string
	var method, organizer, status string
	var attendees []string
	var sequence int
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
		case "METHOD":
			method = strings.ToUpper(strings.TrimSpace(value))
		case "ORGANIZER":
			organizer = mailTo(value)
		case "ATTENDEE":
			if email := mailTo(value); email != "" {
				attendees = append(attendees, email)
			}
		case "SEQUENCE":
			fmt.Sscanf(strings.TrimSpace(value), "%d", &sequence)
		case "STATUS":
			status = strings.ToUpper(strings.TrimSpace(value))
		case "DTSTART":
			start = parseTime(params, value)
		case "DTEND":
			end = parseTime(params, value)
		}
	}
	return calendar.Payload{
		ICS:         raw,
		UID:         uid,
		Title:       title,
		Location:    location,
		Description: description,
		StartsAt:    start,
		EndsAt:      end,
		Method:      method,
		Organizer:   organizer,
		Attendees:   attendees,
		Sequence:    sequence,
		Status:      status,
	}, nil
}

// BuildReply renders an iTIP reply (accept/decline/tentative) to the
// organizer of the event with the given UID.
func BuildReply(uid string, sequence int, organizer, attendee, partstat string, stamp time.Time) string {
	if stamp.IsZero() {
		stamp = time.Now().UTC()
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//Workspace//Workspace//EN\r\n")
	b.WriteString("METHOD:REPLY\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString("UID:" + strings.TrimSpace(uid) + "\r\n")
	b.WriteString("DTSTAMP:" + stamp.Format("20060102T150405Z") + "\r\n")
	fmt.Fprintf(&b, "SEQUENCE:%d\r\n", sequence)
	b.WriteString("ORGANIZER:mailto:" + strings.TrimSpace(organizer) + "\r\n")
	b.WriteString("ATTENDEE;PARTSTAT=" + strings.ToUpper(strings.TrimSpace(partstat)) + ":mailto:" + strings.TrimSpace(attendee) + "\r\n")
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// mailTo strips the mailto: scheme from ORGANIZER/ATTENDEE values.
func mailTo(value string) string {
	value = strings.TrimSpace(value)
	if i := strings.Index(value, ":"); i >= 0 && !strings.Contains(value[:i], "@") {
		value = value[i+1:]
	}
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "mailto:") {
		value = strings.TrimSpace(value[len("mailto:"):])
	}
	return value
}

// BuildInvite renders an iTIP invitation (METHOD:REQUEST or METHOD:CANCEL)
// for an event organized by organizer. Attendees come from the event.
func BuildInvite(e calendar.Event, organizer, method string) string {
	uid := strings.TrimSpace(e.UID)
	if uid == "" {
		uid = e.ID.String()
	}
	stamp := e.UpdatedAt.UTC()
	if stamp.IsZero() {
		stamp = time.Now().UTC()
	}
	status := "CONFIRMED"
	if strings.ToUpper(strings.TrimSpace(method)) == "CANCEL" {
		status = "CANCELLED"
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//Workspace//Workspace//EN\r\n")
	b.WriteString("METHOD:" + strings.ToUpper(strings.TrimSpace(method)) + "\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString("UID:" + uid + "\r\n")
	b.WriteString("DTSTAMP:" + stamp.Format("20060102T150405Z") + "\r\n")
	b.WriteString("DTSTART:" + e.StartsAt.UTC().Format("20060102T150405Z") + "\r\n")
	b.WriteString("DTEND:" + e.EndsAt.UTC().Format("20060102T150405Z") + "\r\n")
	b.WriteString("SUMMARY:" + escapeText(e.Title) + "\r\n")
	if e.Location != "" {
		b.WriteString("LOCATION:" + escapeText(e.Location) + "\r\n")
	}
	if e.Description != "" {
		b.WriteString("DESCRIPTION:" + escapeText(e.Description) + "\r\n")
	}
	fmt.Fprintf(&b, "SEQUENCE:%d\r\n", e.Sequence)
	b.WriteString("STATUS:" + status + "\r\n")
	b.WriteString("ORGANIZER:mailto:" + organizer + "\r\n")
	for _, a := range e.Attendees {
		if strings.TrimSpace(a) == "" {
			continue
		}
		b.WriteString("ATTENDEE;RSVP=TRUE:mailto:" + strings.TrimSpace(a) + "\r\n")
	}
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// Encode serializes an event as a full iCalendar object. Clients (iOS, DAVx5)
// drop VEVENTs that have no UID or DTSTAMP, and require the envelope to be
// versioned, so the minimal object below is enough to be importable.
// A client-supplied UID is preserved verbatim: changing it on read-back
// makes sync clients treat the event as deleted-and-recreated.
func Encode(e calendar.Event) string {
	uid := strings.TrimSpace(e.UID)
	if uid == "" {
		uid = e.ID.String()
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\n")
	b.WriteString("VERSION:2.0\n")
	b.WriteString("PRODID:-//Workspace//Workspace//EN\n")
	b.WriteString("CALSCALE:GREGORIAN\n")
	b.WriteString("BEGIN:VEVENT\n")
	b.WriteString("UID:" + uid + "\n")
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
