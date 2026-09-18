-- SSO identity links: one (provider, subject) maps to exactly one account,
-- so a user can sign in through several providers (Authentik, Google, ...).
-- subject is the opaque provider-side user id; email is cached for display.
CREATE TABLE IF NOT EXISTS sso_identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, subject)
);

CREATE INDEX IF NOT EXISTS sso_identities_user_id_idx ON sso_identities(user_id);