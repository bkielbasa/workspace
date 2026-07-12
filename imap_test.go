package main

import "testing"

func TestParseSeqSingle(t *testing.T) {
	start, end := parseSeq("8", 10)
	if start != 8 || end != 8 {
		t.Fatalf("expected (8,8), got (%d,%d)", start, end)
	}
}

func TestParseSeqRange(t *testing.T) {
	start, end := parseSeq("2:5", 10)
	if start != 2 || end != 5 {
		t.Fatalf("expected (2,5), got (%d,%d)", start, end)
	}
}

func TestParseSeqStar(t *testing.T) {
	start, end := parseSeq("*", 10)
	if start != 10 || end != 10 {
		t.Fatalf("expected (10,10), got (%d,%d)", start, end)
	}
}

func TestMessageSetContains(t *testing.T) {
	for _, tc := range []struct {
		set  string
		n    int
		want bool
	}{
		{"1,3:5,9", 4, true},
		{"1,3:5,9", 2, false},
		{"*:3", 4, true},
		{"*:3", 2, false},
	} {
		if got := messageSetContains(tc.set, tc.n, 5); got != tc.want {
			t.Errorf("messageSetContains(%q, %d) = %v, want %v", tc.set, tc.n, got, tc.want)
		}
	}
}

func TestNextUIDUsesPersistentMaximum(t *testing.T) {
	messages := []Message{{UID: 42}, {UID: 7}, {UID: 99}}
	if got := nextUID(messages); got != 100 {
		t.Fatalf("nextUID = %d, want 100", got)
	}
}

func TestAppendLiteralAndMailboxAttributes(t *testing.T) {
	if size, ok := parseLiteralMarker("{123}"); !ok || size != 123 {
		t.Fatalf("literal marker parsed as (%d, %v)", size, ok)
	}
	if got := mailboxAttributes("Sent"); got != "\\HasNoChildren \\Sent" {
		t.Fatalf("Sent attributes = %q", got)
	}
}

func TestMailboxUpdates(t *testing.T) {
	previous := []Message{{UID: 1}, {UID: 2, Seen: false}}
	current := []Message{{UID: 2, Seen: true}, {UID: 3}}
	updates := mailboxUpdates(previous, current)
	if len(updates) != 2 {
		t.Fatalf("updates = %#v", updates)
	}
	if updates[0] != "* 1 EXPUNGE" || updates[1] != "* 1 FETCH (FLAGS (\\Seen) UID 2)" {
		t.Fatalf("unexpected updates: %#v", updates)
	}
}
