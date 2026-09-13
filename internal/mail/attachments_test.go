package mail

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestBuildAndParseAttachmentsRoundtrip(t *testing.T) {
	atts := []Attachment{
		{Filename: "notes.txt", ContentType: "text/plain", Data: []byte("hello attachments")},
		{Filename: "pixel.png", ContentType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}},
	}
	raw, mimeType := buildRawMessage("a@example.com", "b@example.com", "Files", "mail.local", "<1@mail.local>", time.Now(), "see attached", atts)
	if mimeType != "multipart/mixed" {
		t.Fatalf("mimeType = %q, want multipart/mixed", mimeType)
	}

	infos := ParseAttachments(raw)
	if len(infos) != 2 {
		t.Fatalf("parsed %d attachments, want 2", len(infos))
	}
	if infos[0].Filename != "notes.txt" || infos[0].Size != int64(len("hello attachments")) {
		t.Fatalf("unexpected first attachment: %+v", infos[0])
	}
	if infos[1].Filename != "pixel.png" || infos[1].ContentType != "image/png" {
		t.Fatalf("unexpected second attachment: %+v", infos[1])
	}

	got, err := ExtractAttachment(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data, []byte("hello attachments")) {
		t.Fatalf("extracted data mismatch: %q", got.Data)
	}
	if _, err := ExtractAttachment(raw, 7); err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

func TestParsePlainMessageHasNoAttachments(t *testing.T) {
	raw, mimeType := buildRawMessage("a@example.com", "b@example.com", "Hi", "mail.local", "<2@mail.local>", time.Now(), "just text", nil)
	if mimeType != "text/plain" {
		t.Fatalf("mimeType = %q, want text/plain", mimeType)
	}
	if infos := ParseAttachments(raw); len(infos) != 0 {
		t.Fatalf("parsed %d attachments, want 0", len(infos))
	}
}

func TestParseInboundMixedMessage(t *testing.T) {
	raw := strings.Join([]string{
		"From: x@example.com",
		"To: y@example.com",
		"Subject: inbound",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="b1"`,
		"",
		"--b1",
		"Content-Type: text/plain",
		"",
		"body here",
		"--b1",
		`Content-Type: application/pdf; name="doc.pdf"`,
		"Content-Transfer-Encoding: base64",
		`Content-Disposition: attachment; filename="doc.pdf"`,
		"",
		"aGVsbG8=",
		"--b1--",
		"",
	}, "\r\n")

	infos := ParseAttachments(raw)
	if len(infos) != 1 {
		t.Fatalf("parsed %d attachments, want 1", len(infos))
	}
	if infos[0].Filename != "doc.pdf" || infos[0].Size != 5 {
		t.Fatalf("unexpected attachment: %+v", infos[0])
	}
	got, err := ExtractAttachment(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "hello" {
		t.Fatalf("extracted data = %q, want hello", got.Data)
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename("../../etc/passwd"); got != "passwd" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeFilename("a\"b\\c"); got != "abc" {
		t.Fatalf("got %q", got)
	}
}
