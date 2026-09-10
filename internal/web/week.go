package web

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
)

const weekHourHeight = 48

type weekView struct {
	Label      string
	Query      string
	PrevQuery  string
	NextQuery  string
	IsThisWeek bool
	Hours      []string
	HourStart  int
	HourHeight int
	Days       []weekDay
	StartValue string
	EndValue   string
	// FormOpen reopens the event dialog after a rejected submission so the
	// typed values are not lost.
	FormOpen        bool
	FormAction      string
	FormHeading     string
	FormSubmit      string
	FormTitle       string
	FormLocation    string
	FormDescription string
	// CSS carries the computed geometry. It is served in a nonced <style>
	// element because the CSP forbids inline style attributes.
	CSS template.CSS
}

type weekDay struct {
	Name    string
	Number  int
	Date    string
	IsToday bool
	AllDay  []weekEvent
	Events  []weekEvent
	HasNow  bool
}

type weekEvent struct {
	ID          string
	Title       string
	Location    string
	Description string
	StartValue  string
	EndValue    string
	Hint        string
	TimeLabel   string
	Class       string

	top, height, left, width float64
}

func buildWeek(now time.Time, events []calendar.Event, weekParam string) weekView {
	now = now.In(time.Local)
	start := mondayOf(parseWeekParam(weekParam, now))
	end := start.AddDate(0, 0, 7)

	hourStart, hourEnd := 7, 21
	for _, event := range events {
		if isAllDay(event) || !overlaps(event, start, end) {
			continue
		}
		s := event.StartsAt.In(time.Local)
		en := event.EndsAt.In(time.Local)
		if s.Before(start) {
			s = start
		}
		if en.After(end) {
			en = end
		}
		if s.Hour() < hourStart {
			hourStart = s.Hour()
		}
		endHour := en.Hour()
		if en.Minute() > 0 || en.Second() > 0 {
			endHour++
		}
		if endHour > hourEnd {
			hourEnd = endHour
		}
	}
	if hourStart < 0 {
		hourStart = 0
	}
	if hourEnd > 24 {
		hourEnd = 24
	}
	if hourEnd <= hourStart {
		hourEnd = hourStart + 1
	}

	hours := make([]string, 0, hourEnd-hourStart)
	for hour := hourStart; hour < hourEnd; hour++ {
		hours = append(hours, fmt.Sprintf("%02d:00", hour))
	}

	var css strings.Builder
	fmt.Fprintf(&css, ".week-grid{--hour-height:%dpx;height:%dpx}", weekHourHeight, len(hours)*weekHourHeight)

	days := make([]weekDay, 7)
	placed := 0
	for i := range days {
		day := start.AddDate(0, 0, i)
		days[i] = weekDay{
			Name:    day.Format("Mon"),
			Number:  day.Day(),
			Date:    day.Format("2006-01-02"),
			IsToday: sameDay(day, now),
			AllDay:  allDayOn(events, day),
			Events:  layoutTimed(events, day, hourStart, hourEnd),
		}
		for j := range days[i].Events {
			event := &days[i].Events[j]
			event.Class = fmt.Sprintf("wk-e%d", placed)
			placed++
			fmt.Fprintf(&css, ".%s{top:%.2fpx;height:%.2fpx;left:%.2f%%;width:%.2f%%}",
				event.Class, event.top, event.height, event.left, event.width)
		}
		if days[i].IsToday {
			if top, ok := nowOffset(now, hourStart, hourEnd); ok {
				days[i].HasNow = true
				fmt.Fprintf(&css, ".week-now{top:%.2fpx}", top)
			}
		}
	}

	formStart := nextHour(now)
	if formStart.Before(start) || !formStart.Before(end) {
		formStart = start.Add(9 * time.Hour)
	}

	return weekView{
		Label:      weekLabel(start, end.Add(-time.Nanosecond)),
		Query:      start.Format("2006-01-02"),
		PrevQuery:  start.AddDate(0, 0, -7).Format("2006-01-02"),
		NextQuery:  start.AddDate(0, 0, 7).Format("2006-01-02"),
		IsThisWeek: !now.Before(start) && now.Before(end),
		Hours:      hours,
		HourStart:  hourStart,
		HourHeight: weekHourHeight,
		Days:       days,
		StartValue: formStart.Format("2006-01-02T15:04"),
		EndValue:   formStart.Add(time.Hour).Format("2006-01-02T15:04"),
		CSS:        template.CSS(css.String()),
	}
}

func parseWeekParam(value string, now time.Time) time.Time {
	if value == "" {
		return now
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, now.Location())
	if err != nil {
		return now
	}
	return parsed
}

func mondayOf(t time.Time) time.Time {
	t = startOfDay(t)
	offset := int(t.Weekday() - time.Monday)
	if offset < 0 {
		offset += 7
	}
	return t.AddDate(0, 0, -offset)
}

func startOfDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func weekLabel(start, last time.Time) string {
	if start.Month() == last.Month() && start.Year() == last.Year() {
		return fmt.Sprintf("%d–%d %s %d", start.Day(), last.Day(), start.Format("Jan"), start.Year())
	}
	if start.Year() == last.Year() {
		return fmt.Sprintf("%d %s – %d %s %d", start.Day(), start.Format("Jan"), last.Day(), last.Format("Jan"), start.Year())
	}
	return fmt.Sprintf("%d %s %d – %d %s %d", start.Day(), start.Format("Jan"), start.Year(), last.Day(), last.Format("Jan"), last.Year())
}

func isAllDay(event calendar.Event) bool {
	if event.StartsAt.IsZero() {
		return true
	}
	start := event.StartsAt.In(time.Local)
	end := event.EndsAt.In(time.Local)
	if end.IsZero() {
		return true
	}
	midnight := start.Hour() == 0 && start.Minute() == 0 && start.Second() == 0 &&
		end.Hour() == 0 && end.Minute() == 0 && end.Second() == 0
	return midnight && !end.Before(start.Add(24*time.Hour)) || end.Sub(start) >= 24*time.Hour
}

func overlaps(event calendar.Event, start, end time.Time) bool {
	if event.StartsAt.IsZero() {
		return true
	}
	eventEnd := event.EndsAt
	if eventEnd.IsZero() {
		eventEnd = event.StartsAt.Add(time.Hour)
	}
	return event.StartsAt.Before(end) && eventEnd.After(start)
}

func allDayOn(events []calendar.Event, day time.Time) []weekEvent {
	dayStart := startOfDay(day)
	dayEnd := dayStart.AddDate(0, 0, 1)
	var result []weekEvent
	for _, event := range events {
		if !isAllDay(event) || !overlaps(event, dayStart, dayEnd) {
			continue
		}
		startVal := ""
		if !event.StartsAt.IsZero() {
			startVal = event.StartsAt.In(time.Local).Format("2006-01-02T15:04")
		} else {
			startVal = day.Format("2006-01-02T00:00")
		}
		endVal := ""
		if !event.EndsAt.IsZero() {
			endVal = event.EndsAt.In(time.Local).Format("2006-01-02T15:04")
		} else {
			endVal = day.AddDate(0, 0, 1).Format("2006-01-02T00:00")
		}
		result = append(result, weekEvent{
			ID:          event.ID.String(),
			Title:       event.Title,
			Location:    event.Location,
			Description: event.Description,
			StartValue:  startVal,
			EndValue:    endVal,
			Hint:        eventHint(event, ""),
		})
	}
	return result
}

type timedSlot struct {
	event      calendar.Event
	start, end time.Time
	column     int
	columns    int
}

func layoutTimed(events []calendar.Event, day time.Time, hourStart, hourEnd int) []weekEvent {
	dayStart := startOfDay(day)
	dayEnd := dayStart.AddDate(0, 0, 1)
	windowStart := dayStart.Add(time.Duration(hourStart) * time.Hour)
	windowEnd := dayStart.Add(time.Duration(hourEnd) * time.Hour)

	var slots []timedSlot
	for _, event := range events {
		if isAllDay(event) || !overlaps(event, dayStart, dayEnd) {
			continue
		}
		start := event.StartsAt.In(time.Local)
		end := event.EndsAt.In(time.Local)
		if end.IsZero() {
			end = start.Add(time.Hour)
		}
		if start.Before(dayStart) {
			start = dayStart
		}
		if end.After(dayEnd) {
			end = dayEnd
		}
		if start.Before(windowStart) {
			start = windowStart
		}
		if end.After(windowEnd) {
			end = windowEnd
		}
		if !end.After(start) {
			continue
		}
		slots = append(slots, timedSlot{event: event, start: start, end: end})
	}

	assignColumns(slots)

	placed := make([]weekEvent, 0, len(slots))
	for _, slot := range slots {
		top := slot.start.Sub(windowStart).Minutes() / 60 * weekHourHeight
		height := slot.end.Sub(slot.start).Minutes() / 60 * weekHourHeight
		if height < 22 {
			height = 22
		}
		gap := 1.5
		timeLabel := slot.start.Format("15:04") + "–" + slot.end.Format("15:04")
		startVal := slot.event.StartsAt.In(time.Local).Format("2006-01-02T15:04")
		endVal := ""
		if !slot.event.EndsAt.IsZero() {
			endVal = slot.event.EndsAt.In(time.Local).Format("2006-01-02T15:04")
		} else {
			endVal = slot.event.StartsAt.In(time.Local).Add(time.Hour).Format("2006-01-02T15:04")
		}
		placed = append(placed, weekEvent{
			ID:          slot.event.ID.String(),
			Title:       slot.event.Title,
			Location:    slot.event.Location,
			Description: slot.event.Description,
			StartValue:  startVal,
			EndValue:    endVal,
			Hint:        eventHint(slot.event, timeLabel),
			TimeLabel:   timeLabel,
			top:         top,
			height:      height,
			left:        float64(slot.column) / float64(slot.columns) * 100,
			width:       100.0/float64(slot.columns) - gap,
		})
	}
	return placed
}

func assignColumns(slots []timedSlot) {
	if len(slots) == 0 {
		return
	}
	sortSlots(slots)
	for i := 0; i < len(slots); {
		clusterEnd := slots[i].end
		j := i + 1
		for j < len(slots) && slots[j].start.Before(clusterEnd) {
			if slots[j].end.After(clusterEnd) {
				clusterEnd = slots[j].end
			}
			j++
		}
		packCluster(slots[i:j])
		i = j
	}
}

func sortSlots(slots []timedSlot) {
	for i := 1; i < len(slots); i++ {
		j := i
		for j > 0 && (slots[j].start.Before(slots[j-1].start) || (slots[j].start.Equal(slots[j-1].start) && slots[j].end.After(slots[j-1].end))) {
			slots[j], slots[j-1] = slots[j-1], slots[j]
			j--
		}
	}
}

func packCluster(slots []timedSlot) {
	columns := make([]time.Time, 0)
	maxCol := 0
	for i := range slots {
		col := -1
		for c, until := range columns {
			if !until.After(slots[i].start) {
				col = c
				break
			}
		}
		if col == -1 {
			col = len(columns)
			columns = append(columns, slots[i].end)
		} else {
			columns[col] = slots[i].end
		}
		slots[i].column = col
		if col > maxCol {
			maxCol = col
		}
	}
	for i := range slots {
		slots[i].columns = maxCol + 1
	}
}

func nowOffset(now time.Time, hourStart, hourEnd int) (float64, bool) {
	dayStart := startOfDay(now)
	windowStart := dayStart.Add(time.Duration(hourStart) * time.Hour)
	windowEnd := dayStart.Add(time.Duration(hourEnd) * time.Hour)
	if now.Before(windowStart) || !now.Before(windowEnd) {
		return 0, false
	}
	return now.Sub(windowStart).Minutes() / 60 * weekHourHeight, true
}

func nextHour(now time.Time) time.Time {
	t := now.Truncate(time.Hour).Add(time.Hour)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

func eventHint(event calendar.Event, timeLabel string) string {
	parts := make([]string, 0, 4)
	if event.Title != "" {
		parts = append(parts, event.Title)
	}
	if timeLabel != "" {
		parts = append(parts, timeLabel)
	}
	if event.Location != "" {
		parts = append(parts, event.Location)
	}
	if event.Description != "" {
		parts = append(parts, event.Description)
	}
	return strings.Join(parts, " · ")
}

func weekPath(query string) string {
	if query == "" {
		return "/calendars"
	}
	return "/calendars?week=" + query
}
