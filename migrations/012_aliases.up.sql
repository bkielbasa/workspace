CREATE TABLE IF NOT EXISTS aliases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL REFERENCES domains (id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    destination TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS aliases_address_idx ON aliases (address);
CREATE INDEX IF NOT EXISTS aliases_domain_id_idx ON aliases (domain_id);
