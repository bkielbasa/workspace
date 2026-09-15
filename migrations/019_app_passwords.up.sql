-- Per-device app passwords: revocable credentials for Mail/DAV clients.
-- The plaintext is shown once at generation and only the hash is stored.
-- Web login never accepts them; deleting a user cascades.
CREATE TABLE IF NOT EXISTS app_passwords (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS app_passwords_user_id_idx ON app_passwords(user_id);
