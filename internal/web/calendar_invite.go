package web

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/format/ics"
)

// mailInvite is a meeting invitation found in a message.
type mailInvite struct {
	UID         string
	Method      string
	Organizer   string
	Title       string
	Location    string
	Description string
	Status      string
	StartsAt    time.Time
	EndsAt      time.Time
	Attendees   []string
	Sequence    int
	ICS         string
}

// IsCancel reports whether the invite cancels an event.
func (in *mailInvite) IsCancel() bool {
	return in.Method == "CANCEL" || in.Status == "CANCELLED"
}

// ActionLabel is the button text for the invite box.
func (in *mailInvite) ActionLabel() string {
	if in.IsCancel() {
		return "Remove from calendar"
	}
	return "Add to calendar"
}

// findMailInvite scans a raw message for a text/calendar part and parses it.
// It returns false when the message carries no invitation.
func findMailInvite(raw string) (*mailInvite, bool) {
	for _, part := range calendarParts(raw) {
		payload, err := ics.ParsePayload(part)
		if err != nil || strings.TrimSpace(payload.UID) == "" {
			continue
		}
		return &mailInvite{
			UID:         strings.TrimSpace(payload.UID),
			Method:      payload.Method,
			Organizer:   payload.Organizer,
			Title:       payload.Title,
			Location:    payload.Location,
			Description: payload.Description,
			Status:      payload.Status,
			StartsAt:    payload.StartsAt,
			EndsAt:      payload.EndsAt,
			Attendees:   payload.Attendees,
			Sequence:    payload.Sequence,
			ICS:         part,
		}, true
	}
	return nil, false
}

// calendarParts extracts decoded text/calendar bodies from a raw message,
// covering both single-part messages and nested multiparts.
func calendarParts(raw string) []string {
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return nil
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil
	}
	var out []string
	collectCalendarParts(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), body, &out)
	return out
}

func collectCalendarParts(contentType, encoding string, body []byte, out *[]string) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			p, err := mr.NextPart()
			if err != nil {
				return
			}
			data, err := io.ReadAll(p)
			if err != nil {
				continue
			}
			collectCalendarParts(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), data, out)
		}
	}
	if mediaType == "text/calendar" {
		data, err := io.ReadAll(decodeTransfer(bytes.NewReader(body), encoding))
		if err != nil {
			return
		}
		*out = append(*out, string(data))
	}
}

// parseAttendeeList splits free-form input (comma/semicolon/newline
// separated) into deduplicated emails, dropping the organizer's own address.
func parseAttendeeList(input, ownEmail string) []string {
	seen := map[string]bool{}
	var out []string
	for _, chunk := range strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	}) {
		email := strings.TrimSpace(chunk)
		if addr, err := netmail.ParseAddress(email); err == nil {
			email = addr.Address
		}
		email = strings.Trim(email, "<>")
		email = strings.TrimSpace(email)
		if !strings.Contains(email, "@") {
			continue
		}
		if strings.EqualFold(email, ownEmail) {
			continue
		}
		key := strings.ToLower(email)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, email)
		if len(out) >= 50 {
			break
		}
	}
	return out
}

// inviteMailBody builds the plain-text summary accompanying an invitation.
func inviteMailBody(title, location, description string, start, end time.Time, cancelled bool) string {
	var b strings.Builder
	if cancelled {
		b.WriteString("This meeting has been cancelled:\n\n")
	} else {
		b.WriteString("You are invited to:\n\n")
	}
	b.WriteString(title + "\n")
	if !start.IsZero() {
		b.WriteString("When: " + start.Local().Format("Mon Jan 2, 2006 15:04"))
		if !end.IsZero() {
			b.WriteString(" - " + end.Local().Format("15:04"))
		}
		b.WriteString("\n")
	}
	if location != "" {
		b.WriteString("Where: " + location + "\n")
	}
	if description != "" {
		b.WriteString("\n" + description + "\n")
	}
	return b.String()
}

// inviteWeekPath redirects to the week containing t, falling back to the
// calendar root when the event has no usable time.
func inviteWeekPath(t time.Time) string {
	if t.IsZero() {
		return "/calendars"
	}
	return weekPath(mondayOf(t).Format("2006-01-02"))
}
