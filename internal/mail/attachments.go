package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"path"
	"strings"
	"time"
)

// Upload policy shared by the web compose form and any future API callers.
const (
	// MaxAttachmentCount caps files per message.
	MaxAttachmentCount = 10
	// MaxAttachmentBytes caps a single file.
	MaxAttachmentBytes = 10 << 20
	// MaxAttachmentsTotalBytes caps all files of one message together.
	MaxAttachmentsTotalBytes = 15 << 20
	// maxAttachmentDecodeBytes bounds MIME decoding when listing or
	// downloading attachments of inbound mail (which has no upload cap).
	maxAttachmentDecodeBytes = 32 << 20
)

// Attachment is a file attached to an outgoing message.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// AttachmentInfo describes one attachment of a stored raw message.
type AttachmentInfo struct {
	Filename    string
	ContentType string
	Size        int64
}

// sanitizeFilename strips path components and control characters so the
// value is safe for MIME headers and Content-Disposition.
func sanitizeFilename(name string) string {
	name = path.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		return "attachment"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

// hostnameOrDefault mirrors the fallback used when composing messages.
func hostnameOrDefault(hostname string) string {
	if hostname == "" {
		return "mail.local"
	}
	return hostname
}

// headerGet reads a MIME header case-insensitively.
func headerGet(header map[string][]string, key string) string {
	return textproto.MIMEHeader(header).Get(key)
}

// sanitizeHeaderValue folds newlines so form input cannot inject headers.
func sanitizeHeaderValue(v string) string {
	v = strings.ReplaceAll(v, "\r", " ")
	v = strings.ReplaceAll(v, "\n", " ")
	return strings.TrimSpace(v)
}

func mimeBoundary() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "workspace-boundary"
	}
	return fmt.Sprintf("%x", b)
}

// buildRawMessage renders the full RFC 5322 message. Without attachments it
// stays a plain text message; with attachments it becomes multipart/mixed
// with the text body first and each file as a base64 part.
func buildRawMessage(from, to, subject, hostname, msgID string, now time.Time, body string, atts []Attachment) (raw, mimeType string) {
	from = sanitizeHeaderValue(from)
	to = sanitizeHeaderValue(to)
	subject = sanitizeHeaderValue(subject)
	if msgID == "" {
		msgID = fmt.Sprintf("<%d@%s>", now.UnixNano(), hostnameOrDefault(hostname))
	}
	date := now.Format(time.RFC1123Z)

	if len(atts) == 0 {
		var buf bytes.Buffer
		buf.WriteString("From: " + from + "\r\n")
		buf.WriteString("To: " + to + "\r\n")
		buf.WriteString("Subject: " + subject + "\r\n")
		buf.WriteString("Date: " + date + "\r\n")
		buf.WriteString("Message-ID: " + msgID + "\r\n")
		buf.WriteString("MIME-Version: 1.0\r\n")
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(body)
		return buf.String(), "text/plain"
	}

	boundary := mimeBoundary()
	var buf bytes.Buffer
	buf.WriteString("From: " + from + "\r\n")
	buf.WriteString("To: " + to + "\r\n")
	buf.WriteString("Subject: " + subject + "\r\n")
	buf.WriteString("Date: " + date + "\r\n")
	buf.WriteString("Message-ID: " + msgID + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n")
	buf.WriteString("\r\n")

	w := multipart.NewWriter(&buf)
	_ = w.SetBoundary(boundary)

	text, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"text/plain; charset=UTF-8"},
		"Content-Transfer-Encoding": {"8bit"},
	})
	_, _ = io.WriteString(text, body)

	for _, a := range atts {
		name := sanitizeFilename(a.Filename)
		ct := strings.TrimSpace(a.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		part, _ := w.CreatePart(map[string][]string{
			"Content-Type":              {ct + "; name=\"" + escapeParam(name) + "\""},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {"attachment; filename=\"" + escapeParam(name) + "\""},
		})
		_, _ = writeBase64Lines(part, a.Data)
	}
	_ = w.Close()

	return buf.String(), "multipart/mixed"
}

func escapeParam(v string) string {
	return strings.ReplaceAll(v, `"`, `'`)
}

// writeBase64Lines writes base64 with the RFC 2045 76-char line wrap.
func writeBase64Lines(w io.Writer, data []byte) (int, error) {
	enc := base64.StdEncoding.EncodeToString(data)
	total := 0
	for len(enc) > 76 {
		n, err := io.WriteString(w, enc[:76]+"\r\n")
		total += n
		if err != nil {
			return total, err
		}
		enc = enc[76:]
	}
	n, err := io.WriteString(w, enc+"\r\n")
	return total + n, err
}

// decodePartBody decodes a MIME part according to its Content-Transfer-Encoding.
func decodePartBody(data []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		cleaned := bytes.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, data)
		if decoded, err := base64.StdEncoding.DecodeString(string(cleaned)); err == nil {
			return decoded, nil
		}
		return base64.RawStdEncoding.DecodeString(string(cleaned))
	case "quoted-printable":
		r := quotedprintable.NewReader(bytes.NewReader(data))
		return io.ReadAll(io.LimitReader(r, maxAttachmentDecodeBytes+1))
	default:
		if int64(len(data)) > maxAttachmentDecodeBytes+1 {
			return nil, fmt.Errorf("part too large")
		}
		return data, nil
	}
}

// attachmentPart accumulates one decoded attachment while walking MIME parts.
type attachmentPart struct {
	info AttachmentInfo
	data []byte
}

// walkParts recursively collects attachments from body bytes of mediaType.
func walkParts(body []byte, mediaType string, params map[string]string, out *[]attachmentPart) error {
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil
		}
		mr := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			raw, err := io.ReadAll(io.LimitReader(p, maxAttachmentDecodeBytes+1))
			if err != nil {
				continue
			}
			pType, pParams, _ := parseMediaType(p.Header.Get("Content-Type"))
			if strings.HasPrefix(pType, "multipart/") {
				_ = walkParts(raw, pType, pParams, out)
				continue
			}
			if pType == "message/rfc822" {
				if nested, _, _, err := splitMessage(raw); err == nil {
					_ = collectFromMessage(nested, out)
				}
				continue
			}
			if att, ok := asAttachment(p.Header, pType, pParams, raw); ok {
				*out = append(*out, att)
			}
		}
		return nil
	}
	return nil
}

func parseMediaType(v string) (string, map[string]string, error) {
	if strings.TrimSpace(v) == "" {
		return "text/plain", map[string]string{}, nil
	}
	return mime.ParseMediaType(v)
}

// splitMessage splits raw bytes into header and body at the first blank line.
func splitMessage(raw []byte) (header, body []byte, isCRLF bool, err error) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i], raw[i+4:], true, nil
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[:i], raw[i+2:], false, nil
	}
	return nil, nil, false, fmt.Errorf("no header/body split")
}

// collectFromMessage parses a full message from raw bytes and collects
// its attachments (used for nested message/rfc822 parts).
func collectFromMessage(raw []byte, out *[]attachmentPart) error {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(msg.Body, maxAttachmentDecodeBytes+1))
	if err != nil {
		return err
	}
	mediaType, params, _ := parseMediaType(msg.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return walkParts(body, mediaType, params, out)
	}
	if att, ok := asAttachment(msg.Header, mediaType, params, body); ok {
		*out = append(*out, att)
	}
	return nil
}

// asAttachment decides whether a leaf MIME part is an attachment and
// decodes it. Text parts without a filename are the message body, not files.
func asAttachment(header map[string][]string, mediaType string, params map[string]string, raw []byte) (attachmentPart, bool) {
	disp, dispParams, _ := mime.ParseMediaType(headerGet(header, "Content-Disposition"))
	filename := dispParams["filename"]
	if filename == "" {
		filename = params["name"]
	}

	isAttachment := disp == "attachment" || filename != ""
	if !isAttachment {
		// Nameless non-text parts (images, PDFs, ...) are files too.
		if strings.HasPrefix(mediaType, "text/") || mediaType == "" {
			return attachmentPart{}, false
		}
	}
	if filename == "" {
		filename = "attachment"
	}
	filename = sanitizeFilename(filename)

	ct := mediaType
	if ct == "" {
		ct = "application/octet-stream"
	}

	data, err := decodePartBody(raw, headerGet(header, "Content-Transfer-Encoding"))
	if err != nil || int64(len(data)) > maxAttachmentDecodeBytes {
		return attachmentPart{}, false
	}
	return attachmentPart{
		info: AttachmentInfo{Filename: filename, ContentType: ct, Size: int64(len(data))},
		data: data,
	}, true
}

// collectAttachments parses raw and returns every attachment with its data.
func collectAttachments(raw string) []attachmentPart {
	var out []attachmentPart
	_ = collectFromMessage([]byte(raw), &out)
	for i := range out {
		out[i].info.Filename = sanitizeFilename(out[i].info.Filename)
	}
	return out
}

// ParseAttachments lists the attachments of a stored raw message without
// loading more than bounded decode work.
func ParseAttachments(raw string) []AttachmentInfo {
	parts := collectAttachments(raw)
	infos := make([]AttachmentInfo, 0, len(parts))
	for _, p := range parts {
		infos = append(infos, p.info)
	}
	return infos
}

// ExtractAttachment returns the decoded file at index (as listed by
// ParseAttachments) for download.
func ExtractAttachment(raw string, index int) (Attachment, error) {
	parts := collectAttachments(raw)
	if index < 0 || index >= len(parts) {
		return Attachment{}, fmt.Errorf("attachment not found")
	}
	return Attachment{
		Filename:    parts[index].info.Filename,
		ContentType: parts[index].info.ContentType,
		Data:        parts[index].data,
	}, nil
}
