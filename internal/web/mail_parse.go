package web

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

type mailViewItem struct {
	ID         string
	Sender     string
	SenderName string
	Recipients []string
	Subject    string
	Snippet    string
	Seen       bool
	Flagged    bool
	ReceivedAt time.Time
}

type mailViewDetail struct {
	ID          string
	Sender      string
	SenderName  string
	SenderAddr  string
	Recipients  []string
	Subject     string
	BodyText    string
	BodyHTML    template.HTML
	BodyCSS     template.CSS
	Seen        bool
	Flagged     bool
	ReceivedAt  time.Time
	BoxName     string
	Attachments []mail.AttachmentInfo
	// Invite is set when the message carries a meeting invitation.
	Invite *mailInvite
	// InviteOnCalendar reports whether the invite was already imported.
	InviteOnCalendar bool
}

// composeRecipient is one selectable address for the compose To field:
// every filled email of every contact.
type composeRecipient struct {
	Name  string
	Email string
}

// formatBytes renders a byte count for the attachment list.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	res := htmlTagPattern.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(res), " ")
}

func decodeTransfer(r io.Reader, encoding string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	default:
		return r
	}
}

func cleanSnippet(text string, maxLen int) string {
	fields := strings.Fields(text)
	joined := strings.Join(fields, " ")
	if len(joined) <= maxLen {
		return joined
	}
	return joined[:maxLen] + "..."
}

func parseMailContent(raw string, fallbackMime string) (body string, snippet string) {
	if raw == "" {
		return "", ""
	}

	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		b := strings.TrimSpace(raw)
		return b, cleanSnippet(b, 120)
	}

	contentType := msg.Header.Get("Content-Type")
	encoding := msg.Header.Get("Content-Transfer-Encoding")

	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		bodyBytes = []byte(raw)
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = fallbackMime
	}

	var textBody, htmlBody string
	if strings.HasPrefix(mediaType, "multipart/") {
		textBody, htmlBody = walkBodies(bodyBytes, params["boundary"])
		if textBody == "" && htmlBody != "" {
			textBody = stripHTML(htmlBody)
		}
	} else {
		reader := decodeTransfer(bytes.NewReader(bodyBytes), encoding)
		data, _ := io.ReadAll(reader)
		if strings.HasPrefix(mediaType, "text/html") {
			htmlBody = string(data)
			textBody = stripHTML(htmlBody)
		} else {
			textBody = string(data)
		}
	}

	textBody = strings.TrimSpace(textBody)
	if textBody == "" {
		parts := strings.SplitN(raw, "\r\n\r\n", 2)
		if len(parts) == 2 {
			textBody = strings.TrimSpace(parts[1])
		} else {
			parts = strings.SplitN(raw, "\n\n", 2)
			if len(parts) == 2 {
				textBody = strings.TrimSpace(parts[1])
			} else {
				textBody = strings.TrimSpace(raw)
			}
		}
	}

	snippet = cleanSnippet(textBody, 120)
	return textBody, snippet
}

// walkBodies collects the readable bodies from (possibly nested) multiparts:
// the first text/plain part and the first text/html part at any depth.
// Real-world mail (e.g. Apple) nests alternative inside mixed, which a
// single-level walk misses entirely.
func walkBodies(body []byte, boundary string) (plain, html string) {
	if boundary == "" {
		return "", ""
	}
	var walk func(data []byte, bound string)
	walk = func(data []byte, bound string) {
		if bound == "" {
			return
		}
		mr := multipart.NewReader(bytes.NewReader(data), bound)
		for {
			p, err := mr.NextPart()
			if err != nil {
				return
			}
			pType, pParams, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			raw, err := io.ReadAll(p)
			if err != nil {
				continue
			}
			if strings.HasPrefix(pType, "multipart/") {
				walk(raw, pParams["boundary"])
				continue
			}
			text, _ := io.ReadAll(decodeTransfer(bytes.NewReader(raw), p.Header.Get("Content-Transfer-Encoding")))
			switch {
			case strings.HasPrefix(pType, "text/plain") && plain == "":
				plain = string(text)
			case strings.HasPrefix(pType, "text/html") && html == "":
				html = string(text)
			}
		}
	}
	walk(body, boundary)
	return plain, html
}

// walkTextParts extracts the readable body, preferring plain text.
func walkTextParts(body []byte, boundary string) string {
	plain, html := walkBodies(body, boundary)
	if plain != "" {
		return plain
	}
	return stripHTML(html)
}

// htmlSanitizer is the allowlist applied to HTML mail bodies. Scripts,
// forms, frames and event handlers are dropped; text formatting, tables,
// links and safe inline styling survive, which is what mainstream clients
// render. Style url() values are stripped separately: they are only ever
// decorative backgrounds (content images stay as <img>), and they would
// let senders track opens.
var htmlSanitizer = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowStyling()
	// bgcolor never survives mail filters, but it is only ever a color:
	// let hex values through so the rewrite pass can fold them into the
	// scoped stylesheet as background-color.
	p.AllowAttrs("bgcolor").Matching(regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)).OnElements(
		"table", "td", "tr", "th", "body",
	)
	p.AllowStyles(
		"color", "background", "background-color",
		"font", "font-family", "font-size", "font-style", "font-weight",
		"text-align", "text-decoration", "line-height", "letter-spacing",
		"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
		"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
		"border", "border-top", "border-right", "border-bottom", "border-left",
		"border-collapse", "border-color", "border-radius", "border-spacing",
		"border-style", "border-width",
		"width", "height", "max-width", "min-width",
		"display", "vertical-align", "white-space", "float", "clear",
	).MatchingHandler(func(value string) bool {
		lowered := strings.ToLower(value)
		for _, banned := range []string{
			"url(", "expression", "behavior", "binding",
			"javascript:", "vbscript:", "@import",
		} {
			if strings.Contains(lowered, banned) {
				return false
			}
		}
		return true
	}).Globally()
	return p
}()

var styleURL = regexp.MustCompile(`(?i)(style\s*=\s*"[^"]*)url\s*\(`)

// hexColor matches safe #rgb/#rrggbb/#rrggbbaa values for bgcolor folding.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

func sanitizeHTMLBody(html string) string {
	clean := htmlSanitizer.Sanitize(html)
	return styleURL.ReplaceAllString(clean, "${1}x-url(")
}

// parseMailHTML returns the sanitized HTML body for rich rendering plus a
// scoped stylesheet carrying its inline styles, or "" when the message has
// no HTML part. Callers render the HTML only via template.HTML after this
// sanitization, and the CSS only via template.CSS in a nonced style block.
func parseMailHTML(raw string) (bodyHTML, bodyCSS string) {
	if raw == "" {
		return "", ""
	}
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return "", ""
	}
	contentType := msg.Header.Get("Content-Type")
	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		return "", ""
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", ""
	}
	var html string
	if strings.HasPrefix(mediaType, "multipart/") {
		_, html = walkBodies(bodyBytes, params["boundary"])
	} else if strings.HasPrefix(mediaType, "text/html") {
		data, _ := io.ReadAll(decodeTransfer(bytes.NewReader(bodyBytes), msg.Header.Get("Content-Transfer-Encoding")))
		html = string(data)
	}
	if strings.TrimSpace(html) == "" {
		return "", ""
	}
	clean := sanitizeHTMLBody(html)
	scoped, css := scopeEmailCSS(clean)
	return scoped, css
}

// scopeEmailCSS rewrites inline style attributes into classes scoped under
// .mail-body-html, returning the rewritten fragment plus its stylesheet.
// Some clients, filters and proxies drop inline styles while letting
// stylesheet rules through (or vice versa); emitting both keeps the mail
// readable either way. Pre-existing class attributes are dropped so mail
// can never hijack the app's own classes.
func scopeEmailCSS(fragment string) (htmlOut, cssOut string) {
	doc, err := html.Parse(strings.NewReader("<div>" + fragment + "</div>"))
	if err != nil {
		return fragment, ""
	}
	// Descend to the wrapper div; its children are the fragment nodes.
	wrapper := doc
	var find func(*html.Node)
	find = func(n *html.Node) {
		if wrapper != doc {
			return
		}
		if n.Type == html.ElementNode && n.Data == "div" && n.Parent != nil && n.Parent.Data == "body" {
			wrapper = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if wrapper == doc {
		return fragment, ""
	}
	var css strings.Builder
	counter := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			var style string
			kept := n.Attr[:0]
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "style":
					// Append, never overwrite: a folded bgcolor value may
					// already be in style when bgcolor precedes style.
					if style != "" && !strings.HasSuffix(strings.TrimSpace(style), ";") {
						style += ";"
					}
					style += a.Val
				case "class":
					// dropped: rewritten below
				case "bgcolor":
					// Presentational color attributes never survive
					// sanitizers, but they are just colors: fold a valid
					// hex one into the scoped style.
					if hexColor.MatchString(strings.TrimSpace(a.Val)) {
						if style != "" && !strings.HasSuffix(strings.TrimSpace(style), ";") {
							style += ";"
						}
						style += "background-color: " + strings.TrimSpace(a.Val) + ";"
					}
				default:
					kept = append(kept, a)
				}
			}
			if strings.TrimSpace(style) != "" {
				counter++
				class := fmt.Sprintf("em%d", counter)
				kept = append(kept, html.Attribute{Key: "class", Val: class})
				fmt.Fprintf(&css, ".mail-body-html .%s{%s}\n", class, strings.TrimRight(strings.TrimSpace(style), ";"))
			}
			n.Attr = kept
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for c := wrapper.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	if css.Len() == 0 {
		return fragment, ""
	}
	var out strings.Builder
	for c := wrapper.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&out, c)
	}
	return out.String(), css.String()
}

func parseSender(sender string) (name string, addr string) {
	if a, err := netmail.ParseAddress(sender); err == nil {
		if a.Name != "" {
			return decodeHeader(a.Name), a.Address
		}
		if parts := strings.Split(a.Address, "@"); len(parts) > 0 && parts[0] != "" {
			return parts[0], a.Address
		}
		return a.Address, a.Address
	}
	return decodeHeader(sender), sender
}

// decodeHeader decodes RFC 2047 encoded words (=?charset?Q?...?=) found in
// Subject/From headers. Non-UTF8 charsets fall back to the raw value when
// no charset reader is available.
func decodeHeader(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	dec := new(mime.WordDecoder)
	if decoded, err := dec.DecodeHeader(s); err == nil {
		return decoded
	}
	return s
}

func mailboxIcon(name string) string {
	switch strings.ToUpper(name) {
	case "INBOX":
		return "📥"
	case "SENT":
		return "📤"
	case "DRAFTS":
		return "📝"
	case "TRASH":
		return "🗑"
	case "ARCHIVE":
		return "📁"
	case "SPAM":
		return "⚠️"
	case "IMPORTANT":
		return "🏷"
	case "ALL":
		return "🗂"
	default:
		return "📁"
	}
}

func mailboxTitle(name string) string {
	if strings.EqualFold(name, "INBOX") {
		return "Inbox"
	}
	return name
}

func formatMailDate(t time.Time) string {
	now := time.Now().Local()
	locT := t.Local()
	if locT.Year() == now.Year() && locT.YearDay() == now.YearDay() {
		return locT.Format("15:04")
	}
	if locT.Year() == now.Year() {
		return locT.Format("Jan 2")
	}
	return locT.Format("Jan 2, 2006")
}

func formatDetailDate(t time.Time) string {
	return t.Local().Format("Jan 2, 2006, 15:04")
}

func contactInitialFromSender(sender string) string {
	name, addr := parseSender(sender)
	target := name
	if target == "" {
		target = addr
	}
	for _, r := range target {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return strings.ToUpper(string(r))
		}
	}
	return "M"
}
