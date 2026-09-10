package imap

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

func writeFetchLiteral(w *bufio.Writer, prefix, literal string) error {
	if _, err := fmt.Fprintf(w, "%s\r\n%s)\r\n", prefix, literal); err != nil {
		return err
	}
	return w.Flush()
}

func mailboxAttributes(name string) string {
	attrs := []string{"\\HasNoChildren"}
	switch strings.ToLower(name) {
	case "sent":
		attrs = append(attrs, "\\Sent")
	case "drafts":
		attrs = append(attrs, "\\Drafts")
	case "trash":
		attrs = append(attrs, "\\Trash")
	case "archive":
		attrs = append(attrs, "\\Archive")
	case "spam", "junk":
		attrs = append(attrs, "\\Junk")
	case "all":
		attrs = append(attrs, "\\All")
	case "important":
		attrs = append(attrs, "\\Important")
	}
	return strings.Join(attrs, " ")
}

func parseLiteralMarker(marker string) (int, bool) {
	marker = strings.TrimSpace(marker)
	if len(marker) < 3 || marker[0] != '{' || marker[len(marker)-1] != '}' {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(marker, "{"), "}"))
	return n, err == nil && n >= 0
}

func readLiteral(reader *bufio.Reader, size int) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", err
	}
	return string(data), nil
}

func messageFromAppend(mailboxID uuid.UUID, raw, flags string) *mail.Message {
	message := &mail.Message{
		MailboxID:  mailboxID,
		MessageID:  header(raw, "Message-ID"),
		Sender:     header(raw, "From"),
		Subject:    header(raw, "Subject"),
		InReplyTo:  header(raw, "In-Reply-To"),
		References: header(raw, "References"),
		RawMessage: raw,
		SizeBytes:  int64(len(raw)),
		ReceivedAt: time.Now(),
	}
	to := header(raw, "To")
	if to != "" {
		message.Recipients = strings.Split(to, ",")
	}
	applyFlags(message, "FLAGS", strings.Trim(flags, "() "))
	return message
}

// header returns the first value of the named header in a raw message.
func header(raw, name string) string {
	prefix := strings.ToLower(name) + ":"
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func formatEnvelope(message *mail.Message, date string) string {
	from := imapAddressList([]string{message.Sender})
	return fmt.Sprintf("(%s %s %s %s %s %s NIL NIL %s %s)",
		imapNString(date),
		imapNString(message.Subject),
		from,
		from,
		from,
		imapAddressList(message.Recipients),
		imapNString(message.InReplyTo),
		imapNString(message.MessageID),
	)
}

func imapAddressList(addresses []string) string {
	items := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		local, domain, found := strings.Cut(strings.Trim(address, "<>"), "@")
		if !found || local == "" || domain == "" {
			items = append(items, fmt.Sprintf("(NIL NIL %s NIL)", imapNString(address)))
			continue
		}
		items = append(items, fmt.Sprintf("(NIL NIL %s %s)", imapNString(local), imapNString(domain)))
	}
	if len(items) == 0 {
		return "NIL"
	}
	return "(" + strings.Join(items, " ") + ")"
}

func imapNString(value string) string {
	if value == "" {
		return "NIL"
	}
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\r", "", "\n", "").Replace(value)
	return "\"" + value + "\""
}

func parseSeq(seq string, max int) (int, int) {
	if seq == "" {
		return 1, max
	}

	if seq == "*" {
		return max, max
	}

	if strings.Contains(seq, ":") {
		parts := strings.Split(seq, ":")

		var start int
		if parts[0] == "*" {
			start = max
		} else {
			start, _ = strconv.Atoi(parts[0])
		}

		var end int
		if parts[1] == "*" {
			end = max
		} else {
			end, _ = strconv.Atoi(parts[1])
		}

		if start <= 0 {
			start = 1
		}
		if end <= 0 || end > max {
			end = max
		}
		if start > end {
			start, end = end, start
		}
		return start, end
	}

	// single number
	n, _ := strconv.Atoi(seq)
	if n <= 0 || n > max {
		return 1, max
	}
	return n, n
}

func messageSetContains(set string, n, max int) bool {
	for _, item := range strings.Split(set, ",") {
		bounds := strings.SplitN(item, ":", 2)
		start, end := 0, 0
		if bounds[0] == "*" {
			start = max
		} else {
			start, _ = strconv.Atoi(bounds[0])
		}
		if len(bounds) == 1 {
			end = start
		} else if bounds[1] == "*" {
			end = max
		} else {
			end, _ = strconv.Atoi(bounds[1])
		}
		if start > end {
			start, end = end, start
		}
		if n >= start && n <= end {
			return true
		}
	}
	return false
}

func nextUID(messages []mail.Message) uint64 {
	var max uint64
	for _, message := range messages {
		if message.UID > max {
			max = message.UID
		}
	}
	return max + 1
}

func mailboxUpdates(previous, current []mail.Message) []string {
	currentByUID := make(map[uint64]mail.Message, len(current))
	for _, message := range current {
		currentByUID[message.UID] = message
	}
	updates := make([]string, 0)
	expunged := 0
	for sequence, message := range previous {
		if _, ok := currentByUID[message.UID]; !ok {
			updates = append(updates, fmt.Sprintf("* %d EXPUNGE", sequence+1-expunged))
			expunged++
		}
	}
	if len(previous) != len(current) {
		updates = append(updates, fmt.Sprintf("* %d EXISTS", len(current)))
	}
	previousByUID := make(map[uint64]mail.Message, len(previous))
	for _, message := range previous {
		previousByUID[message.UID] = message
	}
	for sequence, message := range current {
		if before, ok := previousByUID[message.UID]; ok && flagsFor(&before) != flagsFor(&message) {
			updates = append(updates, fmt.Sprintf("* %d FETCH (FLAGS (%s) UID %d)", sequence+1, flagsFor(&message), message.UID))
		}
	}
	return updates
}

func flagsFor(m *mail.Message) string {
	var f []string
	if m.Seen {
		f = append(f, "\\Seen")
	}
	if m.Flagged {
		f = append(f, "\\Flagged")
	}
	if m.Answered {
		f = append(f, "\\Answered")
	}
	if m.Deleted {
		f = append(f, "\\Deleted")
	}
	if m.Draft {
		f = append(f, "\\Draft")
	}
	return strings.Join(f, " ")
}

func countRecent(msgs []mail.Message) int {
	// naive: treat unseen as recent
	c := 0
	for _, m := range msgs {
		if !m.Seen {
			c++
		}
	}
	return c
}

func applyFlags(m *mail.Message, op string, flags string) {
	op = strings.TrimSuffix(strings.ToUpper(op), ".SILENT")
	set := strings.Split(flags, " ")

	apply := func(flag string, val bool) {
		switch flag {
		case "\\Seen":
			m.Seen = val
		case "\\Flagged":
			m.Flagged = val
		case "\\Answered":
			m.Answered = val
		case "\\Deleted":
			m.Deleted = val
		case "\\Draft":
			m.Draft = val
		}
	}

	switch op {
	case "FLAGS":
		// replace
		m.Seen = false
		m.Flagged = false
		m.Answered = false
		m.Deleted = false
		m.Draft = false
		for _, f := range set {
			apply(f, true)
		}

	case "+FLAGS":
		for _, f := range set {
			apply(f, true)
		}

	case "-FLAGS":
		for _, f := range set {
			apply(f, false)
		}
	}
}
