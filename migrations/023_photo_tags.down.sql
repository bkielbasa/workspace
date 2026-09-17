CREATE TABLE IF NOT EXISTS photo_labels (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    label TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, path)
);

DROP TABLE IF EXISTS photo_tags;
