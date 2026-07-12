package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"
)

type Delivery struct {
	users     *Users
	mailboxes *Mailboxes
	mail      *Mail
	outbox    *Outbox
	threads   *Threads
}

func (d *Delivery) Deliver(
	ctx context.Context,
	recipient string,
	message *Message,
) error {
	// normalize recipient (handle <user@domain>, whitespace, etc.)
	rawRecipient := recipient
	recipient = strings.TrimSpace(recipient)
	recipient = strings.Trim(recipient, "<>")

	logWithTrace(ctx, slog.LevelInfo, "delivery lookup",
		"raw_recipient", rawRecipient,
		"normalized_recipient", recipient,
	)

	user, err := d.users.GetByEmail(ctx, recipient)
	if err != nil {
		// fallback to outbound SMTP
		incDeliveryFail(ctx)
		return d.outbox.Enqueue(ctx, recipient, message.RawMessage)
	}

	mailbox, err := d.mailboxes.GetByName(
		ctx,
		user.ID,
		"INBOX",
	)
	if err != nil {
		return fmt.Errorf("find inbox: %w", err)
	}

	logWithTrace(ctx, slog.LevelInfo, "delivery mailbox",
		"user_id", user.ID.String(),
		"mailbox_id", mailbox.ID.String(),
	)

	message.MailboxID = mailbox.ID

	// ✅ ensure message is valid for storage
	if message.ReceivedAt.IsZero() {
		message.ReceivedAt = time.Now()
	}
	if message.MessageID == "" {
		message.MessageID = fmt.Sprintf("<%d@local>", time.Now().UnixNano())
	}

	// ✅ log delivery
	logWithTrace(
		ctx, slog.LevelInfo, "deliver local",
		"recipient", recipient,
		"mailbox", "INBOX",
	)

	if err := d.mail.Append(ctx, message); err != nil {
		incDeliveryFail(ctx)
		return fmt.Errorf("append message: %w", err)
	}

	// assign thread after storing
	if d.threads != nil {
		_ = d.threads.Assign(ctx, message)
	}

	return nil
}

func (d *Delivery) deliverOutbound(recipient string, message *Message) error {
	_, domain, found := strings.Cut(recipient, "@")
	if !found || domain == "" {
		return fmt.Errorf("invalid recipient")
	}
	mx, err := net.LookupMX(domain)
	if err != nil || len(mx) == 0 {
		return fmt.Errorf("lookup MX for %s: %w", domain, err)
	}
	sort.Slice(mx, func(i, j int) bool { return mx[i].Pref < mx[j].Pref })

	var lastErr error
	for _, record := range mx {
		host := strings.TrimSuffix(record.Host, ".")
		if err := deliverSMTP(host, recipient, message); err != nil {
			lastErr = err
			continue
		}
		incDeliveryOK(context.Background())
		return nil
	}
	return fmt.Errorf("deliver to %s: %w", domain, lastErr)
}

func deliverSMTP(host, recipient string, message *Message) error {
	client, err := smtp.Dial(net.JoinHostPort(host, "25"))
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Hello("mail.local"); err != nil {
		return err
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if err := client.Mail(message.Sender); err != nil {
		return err
	}
	if err := client.Rcpt(recipient); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write([]byte(message.RawMessage)); err != nil {
		return err
	}
	return writer.Close()
}
