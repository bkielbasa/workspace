package mail

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

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type userLookup interface {
	GetByEmail(context.Context, string) (*identity.User, error)
}

type aliasLookup interface {
	Resolve(context.Context, string) (string, error)
}

type Delivery struct {
	users     userLookup
	mailboxes *Mailboxes
	mail      *Mail
	outbox    *Outbox
	threads   *Threads
	aliases   aliasLookup
	hostname  string
	tracer    trace.Tracer
}

func NewDelivery(
	users userLookup,
	mailboxes *Mailboxes,
	messages *Mail,
	outbox *Outbox,
	threads *Threads,
	aliases aliasLookup,
	hostname string,
) *Delivery {
	return &Delivery{
		users: users, mailboxes: mailboxes, mail: messages, outbox: outbox,
		threads: threads, aliases: aliases, hostname: hostname,
		tracer: otel.Tracer("delivery"),
	}
}

func (d *Delivery) IsLocal(ctx context.Context, recipient string) error {
	ctx, span := d.tracer.Start(ctx, "delivery.is_local")
	defer span.End()
	recipient = normalizeRecipient(recipient)
	if d.aliases != nil {
		if dest, err := d.aliases.Resolve(ctx, recipient); err == nil && dest != "" {
			recipient = dest
		}
	}
	_, err := d.users.GetByEmail(ctx, recipient)
	return err
}

func (d *Delivery) Deliver(ctx context.Context, recipient string, message *Message) error {
	ctx, span := d.tracer.Start(ctx, "delivery.deliver")
	defer span.End()

	rawRecipient := recipient
	recipient = normalizeRecipient(recipient)
	obs.Log(ctx, slog.LevelInfo, "delivery lookup",
		"raw_recipient", rawRecipient, "normalized_recipient", recipient)

	if d.aliases != nil {
		if dest, err := d.aliases.Resolve(ctx, recipient); err == nil && dest != "" {
			obs.Log(ctx, slog.LevelInfo, "alias resolved",
				"address", recipient, "destination", dest)
			recipient = dest
		}
	}
	user, err := d.users.GetByEmail(ctx, recipient)
	if err != nil {
		return d.outbox.Enqueue(ctx, recipient, message.RawMessage)
	}
	mailbox, err := d.mailboxes.GetByName(ctx, user.ID, "INBOX")
	if err != nil {
		return fmt.Errorf("find inbox: %w", err)
	}
	message.MailboxID = mailbox.ID
	if message.ReceivedAt.IsZero() {
		message.ReceivedAt = time.Now()
	}
	if message.MessageID == "" {
		message.MessageID = fmt.Sprintf("<%d@local>", time.Now().UnixNano())
	}
	if err := d.mail.Append(ctx, message); err != nil {
		return fmt.Errorf("append message: %w", err)
	}
	if d.threads != nil {
		_ = d.threads.Assign(ctx, message)
	}
	return nil
}

func (d *Delivery) DeliverOutbound(recipient string, message *Message) error {
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
		if err := d.deliverSMTP(host, recipient, message); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("deliver to %s: %w", domain, lastErr)
}

func (d *Delivery) deliverSMTP(host, recipient string, message *Message) error {
	client, err := smtp.Dial(net.JoinHostPort(host, "25"))
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Hello(d.hostname); err != nil {
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

func normalizeRecipient(recipient string) string {
	return strings.Trim(strings.TrimSpace(recipient), "<>")
}
