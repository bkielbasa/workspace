package web

import (
	"bytes"
	"html/template"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/google/uuid"
)

// Inline style attributes are dropped by the CSP, so the week page must carry
// its geometry in the nonced <style> element instead.
func TestWeekPageUsesNoncedStyleNotInlineAttributes(t *testing.T) {
	files := os.DirFS("../..")
	tpl, err := template.New("").Funcs(templateFuncs).ParseFS(files,
		"web/templates/layout.html", "web/templates/nav.html", "web/templates/calendars.html")
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 9, 9, 22, 9, 0, 0, time.Local)
	week := buildWeek(time.Date(2026, 9, 10, 14, 30, 0, 0, time.Local), []calendar.Event{{
		ID: uuid.New(), Title: "ddd", StartsAt: start, EndsAt: start.Add(24 * time.Minute),
	}}, "")

	var buf bytes.Buffer
	err = tpl.ExecuteTemplate(&buf, "layout", viewData{
		Title: "Calendar", Section: "calendars", Wide: true,
		Week: week, PageCSS: week.CSS, StyleNonce: "test-nonce",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if strings.Contains(html, "style=\"") {
		t.Errorf("rendered page still uses inline style attributes:\n%s", html)
	}
	if !strings.Contains(html, `<style nonce="test-nonce">`) {
		t.Error("rendered page is missing the nonced style element")
	}
	if strings.Contains(html, "ZgotmplZ") {
		t.Error("generated CSS was escaped away by html/template")
	}
	if !strings.Contains(html, "--hour-height:48px") {
		t.Errorf("hour height never reached the stylesheet:\n%s", html)
	}
	// 22:09 sits well down the grid; a top near zero means placement was lost.
	if !strings.Contains(html, "{top:727.20px") {
		t.Errorf("event was not positioned by the stylesheet:\n%s", html)
	}
}

// The drag-to-create flow reads the day and grid metrics out of the markup.
func TestWeekPageExposesGridMetricsForSelection(t *testing.T) {
	files := os.DirFS("../..")
	tpl, err := template.New("").Funcs(templateFuncs).ParseFS(files,
		"web/templates/layout.html", "web/templates/nav.html", "web/templates/calendars.html")
	if err != nil {
		t.Fatal(err)
	}

	week := buildWeek(time.Date(2026, 9, 10, 14, 30, 0, 0, time.Local), nil, "")
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", viewData{
		Title: "Calendar", Section: "calendars", Week: week, PageCSS: week.CSS,
	}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	for _, want := range []string{
		`data-hour-start="7"`,
		`data-hour-height="48"`,
		`data-date="2026-09-07"`,
		`data-date="2026-09-13"`,
		`<dialog class="modal" id="event-dialog"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered week page missing %s", want)
		}
	}
	if strings.Contains(html, "data-open") {
		t.Error("dialog should stay closed when there is no error")
	}
}

// A rejected submission must come back with the dialog open and values kept.
func TestWeekViewReopensDialogAfterError(t *testing.T) {
	week := buildWeek(time.Date(2026, 9, 10, 14, 30, 0, 0, time.Local), nil, "")
	week.FormOpen = true
	week.FormTitle = "Retro"
	week.StartValue = "2026-09-10T15:00"

	files := os.DirFS("../..")
	tpl, err := template.New("").Funcs(templateFuncs).ParseFS(files,
		"web/templates/layout.html", "web/templates/nav.html", "web/templates/calendars.html")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout", viewData{
		Title: "Calendar", Section: "calendars", Week: week,
		PageCSS: week.CSS, Error: "The event must end after it starts.",
	}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	if !strings.Contains(html, "data-open") {
		t.Error("dialog should reopen after a rejected submission")
	}
	if !strings.Contains(html, `value="Retro"`) {
		t.Error("typed title was lost")
	}
	if !strings.Contains(html, `value="2026-09-10T15:00"`) {
		t.Error("typed start time was lost")
	}

	week.FormLocation = "Room 4"
	week.FormDescription = "Bring notes"
	buf.Reset()
	if err := tpl.ExecuteTemplate(&buf, "layout", viewData{
		Title: "Calendar", Section: "calendars", Week: week, PageCSS: week.CSS,
	}); err != nil {
		t.Fatal(err)
	}
	html = buf.String()
	if !strings.Contains(html, `value="Room 4"`) {
		t.Error("typed location was lost")
	}
	if !strings.Contains(html, ">Bring notes</textarea>") {
		t.Error("typed description was lost")
	}
}
