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
