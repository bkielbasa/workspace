package ics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/format/ics"
	"github.com/google/uuid"
)

func TestEncodeRoundTripsLocationAndDescription(t *testing.T) {
	user := uuid.New()
	raw := ics.Encode(calendar.Event{
		ID:          uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Title:       "Retro",
		Location:    "Room 4, HQ",
		Description: "Bring notes;\nlink: https://example.com",
		StartsAt:    time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		EndsAt:      time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
	})
	if !strings.Contains(raw, "LOCATION:Room 4\\, HQ") {
		t.Fatalf("Encode missing escaped location:\n%s", raw)
	}
	if !strings.Contains(raw, "DESCRIPTION:Bring notes\\;\\nlink: https://example.com") {
		t.Fatalf("Encode missing escaped description:\n%s", raw)
	}

	event, err := ics.Parse(raw, user, "retro")
	if err != nil {
		t.Fatal(err)
	}
	if event.Location != "Room 4, HQ" {
		t.Fatalf("Parse location = %q", event.Location)
	}
	if event.Description != "Bring notes;\nlink: https://example.com" {
		t.Fatalf("Parse description = %q", event.Description)
	}
}

func TestEncodePreservesClientUID(t *testing.T) {
	raw := ics.Encode(calendar.Event{
		ID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		UID:   "client-supplied-uid",
		Title: "Synced",
	})
	if !strings.Contains(raw, "UID:client-supplied-uid") {
		t.Fatalf("Encode must keep client UID:\n%s", raw)
	}
	if strings.Contains(raw, "11111111-1111-1111-1111-111111111111") {
		t.Fatalf("Encode must not leak server ID as UID:\n%s", raw)
	}
}

func TestParseSchedulingFields(t *testing.T) {
	raw := strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"METHOD:REQUEST",
		"BEGIN:VEVENT",
		"UID:evt-123",
		"DTSTAMP:20260913T100000Z",
		"DTSTART:20260914T100000Z",
		"DTEND:20260914T110000Z",
		"SUMMARY:Planning",
		"SEQUENCE:2",
		"STATUS:CONFIRMED",
		"ORGANIZER;CN=Boss:mailto:boss@example.com",
		"ATTENDEE;RSVP=TRUE:mailto:alice@example.com",
		"ATTENDEE:mailto:bob@example.com",
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n")
	ev, err := ics.Parse(raw, uuid.New(), "evt-123")
	if err != nil {
		t.Fatal(err)
	}
	if ev.UID != "evt-123" || ev.Sequence != 2 {
		t.Fatalf("event fields: %+v", ev)
	}
	payload, err := ics.ParsePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Method != "REQUEST" || payload.Organizer != "boss@example.com" || payload.Sequence != 2 || payload.Status != "CONFIRMED" {
		t.Fatalf("scheduling fields: %+v", payload)
	}
	if len(payload.Attendees) != 2 || payload.Attendees[0] != "alice@example.com" {
		t.Fatalf("attendees: %+v", payload.Attendees)
	}
}

func TestBuildInviteRoundtrip(t *testing.T) {
	e := calendar.Event{
		ID:        uuid.New(),
		UID:       "evt-9",
		Title:     "Sync",
		Location:  "Room 1",
		StartsAt:  time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC),
		EndsAt:    time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC),
		Attendees: []string{"a@example.com", "b@example.com"},
		Sequence:  3,
	}
	raw := ics.BuildInvite(e, "me@example.com", "REQUEST")
	for _, want := range []string{"METHOD:REQUEST", "ORGANIZER:mailto:me@example.com", "ATTENDEE;RSVP=TRUE:mailto:a@example.com", "SEQUENCE:3", "UID:evt-9", "STATUS:CONFIRMED"} {
		if !strings.Contains(raw, want) {
			t.Errorf("invite missing %q:\n%s", want, raw)
		}
	}
	cancel := ics.BuildInvite(e, "me@example.com", "CANCEL")
	if !strings.Contains(cancel, "METHOD:CANCEL") || !strings.Contains(cancel, "STATUS:CANCELLED") {
		t.Errorf("cancel malformed:\n%s", cancel)
	}
}

func TestBuildReply(t *testing.T) {
	raw := ics.BuildReply("evt-7", 2, "boss@example.com", "me@example.com", "ACCEPTED", time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))
	for _, want := range []string{"METHOD:REPLY", "UID:evt-7", "SEQUENCE:2", "ORGANIZER:mailto:boss@example.com", "ATTENDEE;PARTSTAT=ACCEPTED:mailto:me@example.com"} {
		if !strings.Contains(raw, want) {
			t.Errorf("reply missing %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "STATUS:") {
		t.Errorf("reply must not carry STATUS:\n%s", raw)
	}
}
