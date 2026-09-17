-- Per-photo labels: the first metadata we store about library items.
-- Keyed by owner + library-relative path (month/name.ext), so renames and
-- month folders stay addressable; rows die with the account.
CREATE TABLE IF NOT EXISTS photo_labels (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    label TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, path)
);
