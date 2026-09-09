package main

import (
	"strings"
	"testing"
)

func TestParseVCardMultiValues(t *testing.T) {
	raw := `BEGIN:VCARD
VERSION:3.0
N:Notabartolo;Alice Rose;Ada;;Dr.
FN:Dr. Alice Rose Notabartolo
ORG:Example Corp;Engineering
TITLE:Staff Engineer
TEL;TYPE=CELL,VOICE:+1 555 0100
TEL;TYPE=WORK:+1 555 0199
EMAIL;TYPE=INTERNET;PREF=1:alice@example.com
EMAIL;TYPE=WORK,INTERNET:alice@example.org
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
	if ct.Company != "Example Corp" || ct.Title != "Staff Engineer" {
		t.Errorf("ORG/TITLE parsed as %q / %q", ct.Company, ct.Title)
	}
	if len(ct.Emails) != 2 {
		t.Fatalf("expected 2 emails, got %d", len(ct.Emails))
	}
	if ct.Email != "alice@example.com" {
		t.Errorf("primary email = %q", ct.Email)
	}
	if ct.Emails[0].Value != "alice@example.com" || ct.Emails[1].Value != "alice@example.org" {
		t.Errorf("email list = %+v", ct.Emails)
	}
	if got := strings.Join(ct.Emails[1].Type, "/"); got != "WORK/INTERNET" {
		t.Errorf("email TYPE = %q", got)
	}
	if got := ct.Emails[0].TypeLabel(); got != "INTERNET" {
		t.Errorf("TypeLabel = %q", got)
	}
	if len(ct.Phones) != 2 {
		t.Fatalf("expected 2 phones, got %d", len(ct.Phones))
	}
	if ct.Phones[0].Value != "+1 555 0100" {
		t.Errorf("first phone = %q", ct.Phones[0].Value)
	}
	if got := strings.Join(ct.Phones[0].Type, "/"); got != "CELL/VOICE" {
		t.Errorf("phone TYPE = %q", got)
	}
	if ct.Phones[1].Header != "TEL;TYPE=WORK" {
		t.Errorf("phone header not preserved: %q", ct.Phones[1].Header)
	}
	if ct.VCard != strings.TrimSpace(raw) {
		t.Errorf("raw vCard not preserved")
	}
	if ct.DisplayName() != "Alice Rose Notabartolo" {
		t.Errorf("displayName = %q", ct.DisplayName())
	}
}

func TestBuildVCardEmitsAllEmailsAndPhones(t *testing.T) {
	ct := Contact{
		FirstName: "Alice", LastName: "Notabartolo", Company: "Example Corp",
		Emails: []VCardField{
			{Value: "alice@example.com", Type: []string{"INTERNET"}},
			{Value: "alice@example.org", Type: []string{"WORK", "INTERNET"}, Header: "EMAIL;TYPE=WORK,INTERNET"},
		},
		Phones: []VCardField{
			{Value: "+1 555 0100", Type: []string{"CELL", "VOICE"}, Header: "TEL;TYPE=CELL,VOICE"},
			{Value: "+1 555 0199", Type: []string{"WORK"}, Header: "TEL;TYPE=WORK"},
		},
	}
	card := buildVCard(ct, "")
	for _, want := range []string{
		"EMAIL;TYPE=INTERNET:alice@example.com",
		"EMAIL;TYPE=WORK,INTERNET:alice@example.org",
		"TEL;TYPE=CELL,VOICE:+1 555 0100",
		"TEL;TYPE=WORK:+1 555 0199",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("missing %q in:\n%s", want, card)
		}
	}
}

func TestBuildVCardPreservesUnknownFieldsOnEdit(t *testing.T) {
	original := `BEGIN:VCARD
VERSION:3.0
N:Notabartolo;Alice;;
FN:Alice Notabartolo
ORG:Example Corp
TEL;TYPE=CELL:+1 555 0100
EMAIL;TYPE=INTERNET:alice@example.com
NOTE:likes coffee
ADR;TYPE=HOME:;;123 Main St;Springfield;;12345;US
BDAY:1970-01-01
END:VCARD
`
	ct := parseVCardFields(original)
	ct.Phones = append(ct.Phones, VCardField{Value: "+1 555 0199", Type: []string{"WORK"}, Header: "TEL;TYPE=WORK"})

	rebuilt := buildVCard(ct, ct.VCard)
	for _, want := range []string{
		"NOTE:likes coffee",
		"ADR;TYPE=HOME:;;123 Main St;Springfield;;12345;US",
		"BDAY:1970-01-01",
		"TEL;TYPE=WORK:+1 555 0199",
	} {
		if !strings.Contains(rebuilt, want) {
			t.Errorf("rebuilt vCard missing %q:\n%s", want, rebuilt)
		}
	}
	again := parseVCardFields(rebuilt)
	if len(again.Phones) != 2 || again.Phones[1].Value != "+1 555 0199" {
		t.Errorf("round trip dropped phone: %+v", again.Phones)
	}
	if !strings.Contains(rebuilt, "VERSION:3.0") || !strings.Contains(rebuilt, "BEGIN:VCARD") {
		t.Errorf("rebuilt vCard missing wrapper")
	}
}

func TestBuildVCardEscapes(t *testing.T) {
	ct := Contact{
		LastName: "Deal;er", FirstName: "Co,ma", Company: "ACME, Inc.",
		Emails: []VCardField{{Value: "a@b.c", Type: []string{"INTERNET"}}},
	}
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

func TestDisplayNameFallbacks(t *testing.T) {
	firstLast := Contact{FirstName: "Grace", LastName: "Hopper", Phones: []VCardField{{Value: "x"}}}
	if firstLast.DisplayName() != "Grace Hopper" {
		t.Errorf("first/last: %q", firstLast.DisplayName())
	}
	corp := Contact{Company: "Acme"}
	if corp.DisplayName() != "Acme" {
		t.Errorf("company: %q", corp.DisplayName())
	}
	emailOnly := Contact{Emails: []VCardField{{Value: "g@h.example"}}}
	if emailOnly.DisplayName() != "g@h.example" {
		t.Errorf("email fallback: %q", emailOnly.DisplayName())
	}
}
