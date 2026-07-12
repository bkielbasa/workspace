package main

import (
    "context"
    "fmt"
    "log"
    "strings"
    "time"
)

func RunSeed(ctx context.Context, users *Users, mail *Mail, mailboxes *Mailboxes) {
    list, err := users.List(ctx)
    if err != nil {
        log.Println("seed: list users:", err)
        return
    }
    if len(list) > 0 {
        return // already seeded
    }

    // deterministic credentials for local/dev
    email := "test@mail.local"
    password := "test123"

    u, err := users.Create(ctx, email, password, "Test User")
    if err != nil {
        log.Println("seed: create user:", err)
        return
    }
    // ensure user is enabled for login
    if _, err := users.db.ExecContext(ctx, `UPDATE users SET enabled = true WHERE id = $1`, u.ID); err != nil {
        log.Println("seed: enable user:", err)
    }
    log.Printf("seed: created user %s / %s", email, password)

    inbox, err := mailboxes.GetByName(ctx, u.ID, "INBOX")
    if err != nil {
        log.Println("seed: inbox:", err)
        return
    }

    now := time.Now()

    msgs := []Message{
        {
            MailboxID: inbox.ID,
            MessageID: "msg-1",
            Sender:    "alice@example.com",
            Recipients: []string{u.Email},
            Subject:   "Welcome to Workspace",
            RawMessage: "From: Alice <alice@example.com>\r\nTo: Test <" + u.Email + ">\r\nSubject: Welcome to Workspace\r\n\r\nHi! This is your first message.",
            ReceivedAt: now.Add(-6 * time.Hour),
        },
        {
            MailboxID: inbox.ID,
            MessageID: "msg-2",
            Sender:    "notifications@service.local",
            Recipients: []string{u.Email},
            Subject:   "Your account was created",
            RawMessage: "From: Service <notifications@service.local>\r\nTo: Test <" + u.Email + ">\r\nSubject: Your account was created\r\n\r\nEverything is ready.",
            ReceivedAt: now.Add(-5 * time.Hour),
        },
        {
            MailboxID: inbox.ID,
            MessageID: "msg-3",
            Sender:    "bob@example.com",
            Recipients: []string{u.Email},
            Subject:   "Meeting tomorrow",
            RawMessage: "From: Bob <bob@example.com>\r\nTo: Test <" + u.Email + ">\r\nSubject: Meeting tomorrow\r\n\r\nAre we still on for tomorrow?",
            ReceivedAt: now.Add(-3 * time.Hour),
        },
        {
            MailboxID: inbox.ID,
            MessageID: "msg-4",
            Sender:    "bob@example.com",
            Recipients: []string{u.Email},
            Subject:   "Re: Meeting tomorrow",
            InReplyTo: "msg-3",
            RawMessage: "From: Bob <bob@example.com>\r\nTo: Test <" + u.Email + ">\r\nSubject: Re: Meeting tomorrow\r\n\r\nLet me know your availability.",
            ReceivedAt: now.Add(-2 * time.Hour),
        },
        {
            MailboxID: inbox.ID,
            MessageID: "msg-5",
            Sender:    "news@newsletter.local",
            Recipients: []string{u.Email},
            Subject:   "Weekly newsletter",
            RawMessage: "From: Newsletter <news@newsletter.local>\r\nTo: Test <" + u.Email + ">\r\nSubject: Weekly newsletter\r\n\r\nHere are this week's updates.",
            ReceivedAt: now.Add(-30 * time.Minute),
        },
    }

    for i := range msgs {
        // Ensure minimal RFC822/MIME headers for client compatibility (Apple Mail)
        raw := msgs[i].RawMessage
        headers := ""

        // Core headers (order matters for some clients)
        if !strings.Contains(raw, "Date:") {
            headers += "Date: " + time.Now().Format(time.RFC1123Z) + "\r\n"
        }
        if !strings.Contains(raw, "Message-ID:") {
            headers += fmt.Sprintf("Message-ID: <%d@mail.local>\r\n", time.Now().UnixNano())
        }

        // Ensure From/To/Subject exist (seed already includes, but keep safe)
        if !strings.Contains(raw, "From:") {
            headers += fmt.Sprintf("From: <%s>\r\n", msgs[i].Sender)
        }
        if !strings.Contains(raw, "To:") {
            headers += fmt.Sprintf("To: <%s>\r\n", strings.Join(msgs[i].Recipients, ", "))
        }
        if !strings.Contains(raw, "Subject:") {
            headers += fmt.Sprintf("Subject: %s\r\n", msgs[i].Subject)
        }

        // MIME headers
        if !strings.Contains(raw, "MIME-Version:") {
            headers += "MIME-Version: 1.0\r\n"
        }
        if !strings.Contains(raw, "Content-Type:") {
            headers += "Content-Type: text/plain; charset=UTF-8\r\n"
        }
        if !strings.Contains(raw, "Content-Transfer-Encoding:") {
            headers += "Content-Transfer-Encoding: 7bit\r\n"
        }

        // Optional but helps some clients
        if !strings.Contains(raw, "Return-Path:") {
            headers += fmt.Sprintf("Return-Path: <%s>\r\n", msgs[i].Sender)
        }

        // Ensure header/body separator exists exactly once
        if !strings.Contains(raw, "\r\n\r\n") {
            raw = raw + "\r\n\r\n"
        }

        msgs[i].RawMessage = headers + raw

        if err := mail.Append(ctx, &msgs[i]); err != nil {
            log.Println("seed: append:", err)
        }
    }
}
