CREATE TABLE mailboxes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    uid_validity BIGINT NOT NULL DEFAULT 1,

    name TEXT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(user_id, name)
);

CREATE INDEX mailboxes_user_id_idx
ON mailboxes(user_id);
