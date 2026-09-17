-- Reusable tags replace one-off captions: the same tag attaches to many
-- photos. Existing captions convert to tags so nothing the user typed is
-- lost (one caption becomes one tag).
CREATE TABLE IF NOT EXISTS photo_tags (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    tag TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, path, tag)
);

INSERT INTO photo_tags (user_id, path, tag)
SELECT user_id, path, left(trim(label), 40)
FROM photo_labels WHERE trim(label) <> ''
ON CONFLICT DO NOTHING;

DROP TABLE photo_labels;
