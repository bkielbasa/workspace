package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	netmail "net/mail"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
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
	users      userLookup
	mailboxes  *Mailboxes
	mail       *Mail
	outbox     *Outbox
	threads    *Threads
	aliases    aliasLookup
	hostname   string
	tracer     trace.Tracer
	rules      RuleRepository
	ruleEngine *RuleEngine
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
		tracer:     otel.Tracer("delivery"),
		ruleEngine: NewRuleEngine(),
	}
}

func (d *Delivery) SetRules(repo RuleRepository) {
	d.rules = repo
}

func extractTextBody(raw string) string {
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	mediaType, params, err := parseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		mediaType = "text/plain"
	}

	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		return ""
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return string(bodyBytes)
		}
		var findTextPlain func(body []byte, mediaType string, params map[string]string) string
		findTextPlain = func(body []byte, mediaType string, params map[string]string) string {
			if strings.HasPrefix(mediaType, "multipart/") {
				bnd := params["boundary"]
				if bnd == "" {
					return ""
				}
				reader := multipart.NewReader(bytes.NewReader(body), bnd)
				for {
					p, err := reader.NextPart()
					if err != nil {
						break
					}
					pBody, err := io.ReadAll(p)
					if err != nil {
						continue
					}
					pType, pParams, _ := parseMediaType(p.Header.Get("Content-Type"))
					if strings.HasPrefix(pType, "multipart/") {
						if res := findTextPlain(pBody, pType, pParams); res != "" {
							return res
						}
					} else if pType == "text/plain" || pType == "" {
						decoded, err := decodePartBody(pBody, p.Header.Get("Content-Transfer-Encoding"))
						if err == nil {
							return string(decoded)
						}
						return string(pBody)
					}
				}
				return ""
			}
			return ""
		}
		if res := findTextPlain(bodyBytes, mediaType, params); res != "" {
			return res
		}
		return string(bodyBytes)
	}

	decoded, err := decodePartBody(bodyBytes, msg.Header.Get("Content-Transfer-Encoding"))
	if err == nil {
		return string(decoded)
	}
	return string(bodyBytes)
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

	if d.rules != nil {
		rules, err := d.rules.ListEnabled(ctx, user.ID)
		if err == nil && len(rules) > 0 {
			body := extractTextBody(message.RawMessage)
			hasAtt := len(ParseAttachments(message.RawMessage)) > 0
			res := d.ruleEngine.Evaluate(rules, message, body, hasAtt)
			if res.Discard {
				obs.Log(ctx, slog.LevelInfo, "message discarded by rule", "message_id", message.MessageID)
				return nil
			}
			if res.MarkRead {
				message.Seen = true
			}
			if res.Star {
				message.Flagged = true
			}
			if res.TargetFolder != "" {
				targetBox, err := d.mailboxes.GetByName(ctx, user.ID, res.TargetFolder)
				if err != nil {
					targetBox, err = d.mailboxes.Create(ctx, user.ID, res.TargetFolder)
				}
				if err == nil && targetBox != nil {
					message.MailboxID = targetBox.ID
				}
			}
		}
	}

	if message.MailboxID == uuid.Nil {
		mailbox, err := d.mailboxes.GetByName(ctx, user.ID, "INBOX")
		if err != nil {
			return fmt.Errorf("find inbox: %w", err)
		}
		message.MailboxID = mailbox.ID
	}

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
