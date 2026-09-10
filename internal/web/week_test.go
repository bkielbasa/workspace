package web

import (
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/google/uuid"
)

func TestMondayOfUsesISOWeek(t *testing.T) {
	sunday := time.Date(2026, 9, 13, 15, 0, 0, 0, time.Local)
	got := mondayOf(sunday)
	if want := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local); !got.Equal(want) {
		t.Fatalf("mondayOf(Sunday) = %v, want %v", got, want)
	}
}

func TestBuildWeekPlacesTimedAndAllDayEvents(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 30, 0, 0, time.Local)
	monday := time.Date(2026, 9, 7, 9, 0, 0, 0, time.Local)
	events := []calendar.Event{
		{
			ID:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			Title:    "Standup",
			StartsAt: monday,
			EndsAt:   monday.Add(30 * time.Minute),
		},
		{
			ID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			Title:    "Away",
			StartsAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.Local),
			EndsAt:   time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local),
		},
		{
			ID:       uuid.MustParse("33333333-3333-3333-3333-333333333333"),
			Title:    "Overlap A",
			StartsAt: monday.Add(time.Hour),
			EndsAt:   monday.Add(2 * time.Hour),
		},
		{
			ID:       uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			Title:    "Overlap B",
			StartsAt: monday.Add(90 * time.Minute),
			EndsAt:   monday.Add(3 * time.Hour),
		},
	}

	week := buildWeek(now, events, "2026-09-09")
	if week.Query != "2026-09-07" {
		t.Fatalf("week query = %q, want 2026-09-07", week.Query)
	}
	if week.Days[3].IsToday != true {
		t.Fatalf("Thursday should be today")
	}
	if got := len(week.Days[0].Events); got != 3 {
		t.Fatalf("Monday timed events = %d, want 3", got)
	}
	if week.Days[0].Events[0].TimeLabel != "09:00–09:30" {
		t.Fatalf("standup label = %q", week.Days[0].Events[0].TimeLabel)
	}

	var overlapping int
	for _, event := range week.Days[0].Events {
		if event.Title == "Overlap A" || event.Title == "Overlap B" {
			if event.width > 60 {
				t.Fatalf("%s should share the column, width=%.2f%%", event.Title, event.width)
			}
			overlapping++
		}
	}
	if overlapping != 2 {
		t.Fatalf("overlapping events = %d, want 2", overlapping)
	}

	// Geometry must reach the browser as a stylesheet: the CSP blocks inline
	// style attributes, so a missing rule here means an unpositioned event.
	css := string(week.CSS)
	if !strings.Contains(css, ".week-grid{--hour-height:48px;") {
		t.Fatalf("week CSS missing grid metrics: %s", css)
	}
	for _, event := range week.Days[0].Events {
		if event.Class == "" || !strings.Contains(css, "."+event.Class+"{top:") {
			t.Fatalf("event %q has no CSS rule in: %s", event.Title, css)
		}
	}

	if len(week.Days[1].AllDay) != 1 || week.Days[1].AllDay[0].Title != "Away" {
		t.Fatalf("Tuesday all-day = %+v", week.Days[1].AllDay)
	}
	if len(week.Days[2].AllDay) != 1 {
		t.Fatalf("Wednesday should still show the spanning all-day event")
	}
	if len(week.Days[3].AllDay) != 0 {
		t.Fatalf("Thursday should not include an event that ended at Wednesday midnight")
	}
	if !week.Days[3].HasNow {
		t.Fatal("today should show a now marker")
	}
}
