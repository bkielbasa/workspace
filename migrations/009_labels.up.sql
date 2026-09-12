CREATE TABLE IF NOT EXISTS labels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS message_labels (
    message_id UUID NOT NULL,
    label_id UUID NOT NULL,
    PRIMARY KEY (message_id, label_id)
);

CREATE INDEX IF NOT EXISTS idx_labels_user ON labels(user_id);
