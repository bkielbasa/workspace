CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    mailbox_id UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,

    -- IMAP sequence number within the mailbox
    uid BIGSERIAL,

    -- RFC 5322 Message-ID header
    message_id TEXT,

    -- Envelope
    sender TEXT NOT NULL,
    recipients TEXT[] NOT NULL DEFAULT '{}',

    -- Common headers
    subject TEXT,

    -- Threading headers
    in_reply_to TEXT,
    references_header TEXT,
    thread_id UUID,

    -- Message content
    raw_message TEXT NOT NULL,

    -- MIME information
    mime_type TEXT,
    charset TEXT,

    -- Size
    size_bytes BIGINT NOT NULL DEFAULT 0,

    -- IMAP flags
    seen BOOLEAN NOT NULL DEFAULT FALSE,
    flagged BOOLEAN NOT NULL DEFAULT FALSE,
    answered BOOLEAN NOT NULL DEFAULT FALSE,
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    draft BOOLEAN NOT NULL DEFAULT FALSE,

    -- Delivery metadata
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX messages_mailbox_id_idx
ON messages(mailbox_id);

CREATE INDEX messages_message_id_idx
ON messages(message_id);

CREATE INDEX messages_received_at_idx
ON messages(received_at DESC);

CREATE INDEX messages_sender_idx
ON messages(sender);

CREATE INDEX idx_messages_mailbox_uid
ON messages (mailbox_id, uid);

CREATE INDEX idx_messages_thread
ON messages(thread_id);
