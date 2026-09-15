package appleprofile

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestBuildWithoutPasswordPrompts(t *testing.T) {
	out, err := Build("contact@cloudlift.run", "mail.cloudlift.run", "dav.cloudlift.run", "cloudlift.run", "")
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	for _, want := range []string{
		"<string>com.apple.mail.managed</string>",
		"<string>com.apple.carddav.account</string>",
		"<string>com.apple.caldav.account</string>",
		"<string>com.apple.webclip.managed</string>",
		"<string>https://cloudlift.run/files</string>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"<key>IncomingPassword</key>", "<key>OutgoingPassword</key>",
		"<key>CalDAVPassword</key>", "<key>CardDAVPassword</key>",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("passwordless profile must not contain %q", forbidden)
		}
	}
}

func TestBuildWithPasswordSuppressesPrompts(t *testing.T) {
	out, err := Build("contact@cloudlift.run", "mail.cloudlift.run", "dav.cloudlift.run", "cloudlift.run", "secret-device-pass")
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	for _, want := range []string{
		"<key>IncomingPassword</key>",
		"<key>OutgoingPassword</key>",
		"<key>CalDAVPassword</key>",
		"<key>CardDAVPassword</key>",
		"<string>secret-device-pass</string>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestBuildRequiresAddressAndHosts(t *testing.T) {
	for _, tc := range [][5]string{
		{"", "mail.cloudlift.run", "dav.cloudlift.run", "cloudlift.run", ""},
		{"not-an-address", "mail.cloudlift.run", "dav.cloudlift.run", "cloudlift.run", ""},
		{"a@b.c", "", "dav.cloudlift.run", "cloudlift.run", ""},
		{"a@b.c", "mail.cloudlift.run", "", "cloudlift.run", ""},
		{"a@b.c", "mail.cloudlift.run", "dav.cloudlift.run", "", ""},
	} {
		if _, err := Build(tc[0], tc[1], tc[2], tc[3], tc[4]); err == nil {
			t.Errorf("expected error for %+v", tc)
		}
	}
}

func TestBuildWellFormedAndStable(t *testing.T) {
	first, err := Build("a@b.c", "m.h", "d.h", "w.h", "pw")
	if err != nil {
		t.Fatal(err)
	}
	dec := xml.NewDecoder(strings.NewReader(string(first)))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("not well-formed: %v", err)
		}
	}
	second, err := Build("a@b.c", "m.h", "d.h", "w.h", "other-pw")
	if err != nil {
		t.Fatal(err)
	}
	// UUIDs derive from the address only, so reinstalling replaces.
	strip := func(s string) string {
		s = strings.ReplaceAll(s, "other-pw", "pw")
		return s
	}
	if strip(string(first)) != strip(string(second)) {
		t.Errorf("identifiers not stable across password changes")
	}
}
