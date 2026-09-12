ALTER TABLE contacts
    ADD COLUMN IF NOT EXISTS first_name TEXT,
    ADD COLUMN IF NOT EXISTS last_name TEXT,
    ADD COLUMN IF NOT EXISTS company TEXT,
    ADD COLUMN IF NOT EXISTS title TEXT,
    ADD COLUMN IF NOT EXISTS phone TEXT,
    ADD COLUMN IF NOT EXISTS vcard TEXT NOT NULL DEFAULT '';

-- One contact per primary email, but allow multiple contacts without an
-- email (phone-only vCards from CardDAV clients).
DROP INDEX IF EXISTS idx_contacts_user_email;
CREATE UNIQUE INDEX IF NOT EXISTS idx_contacts_user_email
ON contacts (user_id, email) WHERE email <> '';
