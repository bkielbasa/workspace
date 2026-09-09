package main

import (
	"strings"
	"testing"
)

func TestParseVCardFields(t *testing.T) {
	raw := `BEGIN:VCARD
VERSION:3.0
N:Notabartolo;Alice Rose;Ada;;Dr.
FN:Dr. Alice Rose Notabartolo
ORG:Example Corp;Engineering
TITLE:Staff Engineer
TEL;TYPE=CELL,VOICE:+1 555 0100
TEL;TYPE=WORK:+1 555 0199
EMAIL;TYPE=INTERNET:alice@example.com
NOTE:likes coffee
BDAY:1970-01-01
END:VCARD
`
	ct := parseVCardFields(raw)
	if ct.FirstName != "Alice Rose" || ct.LastName != "Notabartolo" {
		t.Errorf("N parsed as first=%q last=%q", ct.FirstName, ct.LastName)
	}
	if ct.Name != "Dr. Alice Rose Notabartolo" {
		t.Errorf("FN parsed as %q", ct.Name)
	}
	if ct.Company != "Example Corp" {
		t.Errorf("ORG parsed as %q", ct.Company)
	}
	if ct.Title != "Staff Engineer" {
		t.Errorf("TITLE parsed as %q", ct.Title)
	}
	if ct.Phone != "+1 555 0100" {
		t.Errorf("TEL should prefer CELL, got %q", ct.Phone)
	}
	if ct.Email != "alice@example.com" {
		t.Errorf("EMAIL parsed as %q", ct.Email)
	}
	if ct.VCard != strings.TrimSpace(raw) {
		t.Errorf("raw vCard not preserved")
	}
	if ct.DisplayName() != "Alice Rose Notabartolo" {
		t.Errorf("displayName = %q", ct.DisplayName())
	}
}

func TestBuildVCardPreservesUnknownFieldsOnEdit(t *testing.T) {
	original := `BEGIN:VCARD
VERSION:3.0
N:Notabartolo;Alice;;
FN:Alice Notabartolo
ORG:Example Corp
EMAIL;TYPE=INTERNET:alice@example.com
NOTE:likes coffee
BDAY:1970-01-01
END:VCARD
`
	ct := parseVCardFields(original)
	ct.Phone = "+1 555 0100"

	rebuilt := buildVCard(ct, ct.VCard)
	for _, want := range []string{"NOTE:likes coffee", "BDAY:1970-01-01", "TEL;TYPE=CELL,VOICE:+1 555 0100", "FN:Alice Notabartolo"} {
		if !strings.Contains(rebuilt, want) {
			t.Errorf("rebuilt vCard missing %q:\n%s", want, rebuilt)
		}
	}
	if strings.Contains(rebuilt, "ORG:Example Corp") {
		// ORG is a base field and gets regenerated from ct.Company
	}
	again := parseVCardFields(rebuilt)
	if again.Phone != "+1 555 0100" {
		t.Errorf("round trip dropped phone: %+v", again)
	}
	if !strings.Contains(rebuilt, "VERSION:3.0") || !strings.Contains(rebuilt, "BEGIN:VCARD") || !strings.Contains(rebuilt, "END:VCARD") {
		t.Errorf("rebuilt vCard missing wrapper")
	}
}

func TestBuildVCardEscapes(t *testing.T) {
	ct := Contact{LastName: "Deal;er", FirstName: "Co,ma", Company: "ACME, Inc.", Email: "a@b.c"}
	card := buildVCard(ct, "")
	if !strings.Contains(card, `N:Deal\;er;Co\,ma;;;`) {
		t.Errorf("N not escaped: %s", card)
	}
	if !strings.Contains(card, `ORG:ACME\, Inc.`) {
		t.Errorf("ORG not escaped: %s", card)
	}
	rt := parseVCardFields(card)
	if rt.LastName != "Deal;er" || rt.FirstName != "Co,ma" || rt.Company != "ACME, Inc." {
		t.Errorf("escape round trip failed: %+v", rt)
	}
}

func TestUnfoldFoldedVCard(t *testing.T) {
	raw := "BEGIN:VCARD\nNOTE:some\n text\n extra lines\nEND:VCARD\n"
	ct := parseVCardFields(raw)
	if joined := strings.Join(unfoldVCard(ct.VCard), "\n"); !strings.Contains(joined, "NOTE:sometextextra lines") {
		t.Errorf("folded lines not unfolded: %q", joined)
	}
}
