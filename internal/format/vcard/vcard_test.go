package vcard_test

import (
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/format/vcard"
)

func TestEncodePreservesFieldKind(t *testing.T) {
	card := vcard.Encode(contacts.Contact{
		FirstName: "Ada",
		LastName:  "Lovelace",
		Emails: []contacts.Field{
			{Value: "ada@home.example", Type: []string{"HOME"}},
			{Value: "ada@work.example", Type: []string{"WORK"}},
			{Value: "ada@school.example", Type: []string{"SCHOOL"}},
		},
		Phones: []contacts.Field{
			{Value: "+1 555 0100", Type: []string{"HOME"}},
		},
	}, "")

	for _, want := range []string{
		"EMAIL;TYPE=INTERNET,HOME:ada@home.example",
		"EMAIL;TYPE=INTERNET,WORK:ada@work.example",
		"EMAIL;TYPE=INTERNET,SCHOOL:ada@school.example",
		"TEL;TYPE=HOME,VOICE:+1 555 0100",
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("Encode() missing %q in:\n%s", want, card)
		}
	}

	parsed := vcard.Parse(card)
	if got, want := parsed.Emails[1].Kind(), contacts.KindWork; got != want {
		t.Fatalf("parsed work email Kind() = %q, want %q", got, want)
	}
	if got, want := parsed.Phones[0].KindLabel(), "Personal"; got != want {
		t.Fatalf("parsed phone KindLabel() = %q, want %q", got, want)
	}
}
