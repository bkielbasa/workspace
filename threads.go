package main

import (
    "context"
    "database/sql"
    "fmt"
    "strings"

    "github.com/google/uuid"
)

type Thread struct {
    ID      uuid.UUID
    Subject string
    Count   int
}

type Threads struct {
    db *sql.DB
}

func (t *Threads) List(ctx context.Context, userID uuid.UUID) ([]Thread, error) {
    rows, err := t.db.QueryContext(ctx, `
        SELECT
            COALESCE(thread_id, id) as thread_id,
            subject,
            COUNT(*)
        FROM messages m
        JOIN mailboxes mb ON m.mailbox_id = mb.id
        WHERE mb.user_id = $1
        GROUP BY thread_id, subject
        ORDER BY MAX(received_at) DESC
    `, userID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var res []Thread
    for rows.Next() {
        var th Thread
        if err := rows.Scan(&th.ID, &th.Subject, &th.Count); err != nil {
            return nil, err
        }
        res = append(res, th)
    }
    return res, rows.Err()
}

func (t *Threads) Assign(ctx context.Context, msg *Message) error {
    var threadID uuid.UUID

    // 1. try In-Reply-To
    if msg.InReplyTo != "" {
        _ = t.db.QueryRowContext(ctx, `
            SELECT thread_id FROM messages WHERE message_id = $1 LIMIT 1
        `, msg.InReplyTo).Scan(&threadID)
    }

    // 2. try References (first match)
    if threadID == uuid.Nil && msg.References != "" {
        refs := strings.Split(msg.References, " ")
        for _, r := range refs {
            if r == "" {
                continue
            }
            err := t.db.QueryRowContext(ctx, `
                SELECT thread_id FROM messages WHERE message_id = $1 LIMIT 1
            `, r).Scan(&threadID)
            if err == nil && threadID != uuid.Nil {
                break
            }
        }
    }

    // 3. fallback to normalized subject
    if threadID == uuid.Nil {
        subj := normalizeSubject(msg.Subject)
        _ = t.db.QueryRowContext(ctx, `
            SELECT thread_id FROM messages WHERE subject = $1 AND thread_id IS NOT NULL LIMIT 1
        `, subj).Scan(&threadID)
    }

    if threadID == uuid.Nil {
        threadID = uuid.New()
    }

    _, err := t.db.ExecContext(ctx, `
        UPDATE messages SET thread_id = $2 WHERE id = $1
    `, msg.ID, threadID)

    if err != nil {
        return fmt.Errorf("assign thread: %w", err)
    }

    return nil
}

func normalizeSubject(s string) string {
    s = strings.TrimSpace(s)
    lower := strings.ToLower(s)
    for strings.HasPrefix(lower, "re:") || strings.HasPrefix(lower, "fwd:") {
        if strings.HasPrefix(lower, "re:") {
            s = strings.TrimSpace(s[3:])
        } else if strings.HasPrefix(lower, "fwd:") {
            s = strings.TrimSpace(s[4:])
        }
        lower = strings.ToLower(s)
    }
    return s
}
