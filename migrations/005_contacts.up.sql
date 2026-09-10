CREATE TABLE contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,

    email TEXT NOT NULL,
    emails TEXT,
    phones TEXT,
    first_name TEXT,
    last_name TEXT,
    company TEXT,
    title TEXT,
    name TEXT,

    etag TEXT NOT NULL,
    vcard TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_contacts_user_email
ON contacts(user_id, email);
