package web

import (
	"strings"
	"testing"
	"time"
)

const inviteRaw = "From: boss@example.com\r\n" +
	"To: alice@example.com\r\n" +
	"Subject: Planning\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b9\"\r\n" +
	"\r\n" +
	"--b9\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"Join me.\r\n" +
	"--b9\r\n" +
	"Content-Type: text/calendar; method=REQUEST; charset=UTF-8\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"QkVHSU46VkNBTEVOREFSDQpWRVJTSU9OOjIuMA0KTUVUSE9EOlJFUVVFU1QNCkJFR0lOOlZFVkVOVA0KVUlEOmludml0ZS0xDQpEVFNUQVJUOjIwMjYwOTE0VDEwMDAwMFoNCkRURU5EOjIwMjYwOTE0VDExMDAwMFoNClNVTU1BUlk6UGxhbm5pbmcNClNFUVVFTkNFOjENCk9SR0FOSVpFUjptYWlsdG86Ym9zc0BleGFtcGxlLmNvbQ0KQVRURU5ERUU6bWFpbHRvOmFsaWNlQGV4YW1wbGUuY29tDQpFTkQ6VkVWRU5UDQpFTkQ6VkNBTEVOREFS\r\n" +
	"--b9--\r\n"

func TestFindMailInvite(t *testing.T) {
	invite, ok := findMailInvite(inviteRaw)
	if !ok {
		t.Fatal("expected invite to be found")
	}
	if invite.UID != "invite-1" || invite.Method != "REQUEST" || invite.Organizer != "boss@example.com" {
		t.Fatalf("unexpected invite: %+v", invite)
	}
	if invite.Title != "Planning" || invite.Sequence != 1 {
		t.Fatalf("unexpected details: %+v", invite)
	}
	if len(invite.Attendees) != 1 || invite.Attendees[0] != "alice@example.com" {
		t.Fatalf("unexpected attendees: %+v", invite.Attendees)
	}
	if invite.ActionLabel() != "Add to calendar" || invite.IsCancel() {
		t.Fatalf("unexpected action state: %+v", invite)
	}
}

func TestFindMailInviteAbsent(t *testing.T) {
	if _, ok := findMailInvite("From: a@b.c\r\n\r\nplain body"); ok {
		t.Fatal("expected no invite in plain message")
	}
}

func TestFindMailInviteCancel(t *testing.T) {
	raw := "Content-Type: text/calendar\r\n\r\nBEGIN:VCALENDAR\r\nMETHOD:CANCEL\r\nBEGIN:VEVENT\r\nUID:x-1\r\nSUMMARY:Gone\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	invite, ok := findMailInvite(raw)
	if !ok {
		t.Fatal("expected cancel invite to be found")
	}
	if !invite.IsCancel() || invite.ActionLabel() != "Remove from calendar" {
		t.Fatalf("unexpected cancel state: %+v", invite)
	}
}

func TestParseAttendeeList(t *testing.T) {
	got := parseAttendeeList("Bob <bob@example.com>, alice@example.com\nalice@example.com; bad-entry, ME@example.com", "me@example.com")
	if len(got) != 2 || got[0] != "bob@example.com" || got[1] != "alice@example.com" {
		t.Fatalf("attendees = %q", got)
	}
}

func TestInviteWeekPath(t *testing.T) {
	when := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	if got := inviteWeekPath(when); !strings.HasPrefix(got, "/calendars?week=") {
		t.Fatalf("week path = %q", got)
	}
	if got := inviteWeekPath(time.Time{}); got != "/calendars" {
		t.Fatalf("empty week path = %q", got)
	}
}

func TestFindMailInviteNestedAppleShape(t *testing.T) {
	raw := strings.Join([]string{
		"From: Boss <boss@example.com>",
		"To: alice@example.com",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="outer"`,
		"",
		"--outer",
		`Content-Type: multipart/alternative; boundary="inner"`,
		"",
		"--inner",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		"see attached",
		"--inner--",
		"--outer",
		"Content-Type: text/calendar; method=REQUEST",
		"Content-Transfer-Encoding: Base64",
		`Content-Disposition: attachment; filename="invite.ics"`,
		"",
		"QkVHSU46VkNBTEVOREFSDQpWRVJTSU9OOjIuMA0KTUVUSE9EOlJFUVVFU1QNCkJFR0lOOlZFVkVOVA0KVUlEOm5lc3RlZC0xDQpEVFNUQVJUOjIwMjYwOTE0VDE5MDAwMFoNCkRURU5EOjIwMjYwOTE0VDIwMDAwMFoNClNVTU1BUlk6VGVzdCB6YXByb3N6ZW5pYQ0KU0VRVUVOQ0U6MA0KT1JHQU5JWkVSOm1haWx0bzpib3NzQGV4YW1wbGUuY29tDQpFTkQ6VkVWRU5UDQpFTkQ6VkNBTEVOREFS",
		"--outer--",
		"",
	}, "\r\n")
	invite, ok := findMailInvite(raw)
	if !ok {
		t.Fatal("expected nested invite to be found")
	}
	if invite.UID != "nested-1" || invite.Title != "Test zaproszenia" {
		t.Fatalf("unexpected invite: %+v", invite)
	}
	body, _ := parseMailContent(raw, "")
	if !strings.Contains(body, "see attached") {
		t.Fatalf("nested body = %q", body)
	}
}
