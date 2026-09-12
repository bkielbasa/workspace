-- A contact can hold several emails and phones (vCard TEL/EMAIL list).
-- Ensure phone column exists so queries and backfill don't fail if table was created without it
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS phone TEXT;

DO $$
BEGIN
    -- Convert emails to JSONB if it was created as text
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'contacts' AND column_name = 'emails' AND data_type = 'text'
    ) THEN
        ALTER TABLE contacts ALTER COLUMN emails TYPE JSONB USING (
            CASE 
                WHEN emails IS NULL OR emails = '' THEN '[]'::jsonb
                WHEN emails ~ '^\s*\[.*\]\s*$' THEN emails::jsonb
                ELSE jsonb_build_array(jsonb_build_object('value', emails, 'type', jsonb_build_array('INTERNET')))
            END
        );
        ALTER TABLE contacts ALTER COLUMN emails SET DEFAULT '[]'::jsonb;
        ALTER TABLE contacts ALTER COLUMN emails SET NOT NULL;
    ELSE
        ALTER TABLE contacts ADD COLUMN IF NOT EXISTS emails JSONB NOT NULL DEFAULT '[]';
    END IF;

    -- Convert phones to JSONB if it was created as text
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'contacts' AND column_name = 'phones' AND data_type = 'text'
    ) THEN
        ALTER TABLE contacts ALTER COLUMN phones TYPE JSONB USING (
            CASE 
                WHEN phones IS NULL OR phones = '' THEN '[]'::jsonb
                WHEN phones ~ '^\s*\[.*\]\s*$' THEN phones::jsonb
                ELSE jsonb_build_array(jsonb_build_object('value', phones, 'type', jsonb_build_array('CELL', 'VOICE')))
            END
        );
        ALTER TABLE contacts ALTER COLUMN phones SET DEFAULT '[]'::jsonb;
        ALTER TABLE contacts ALTER COLUMN phones SET NOT NULL;
    ELSE
        ALTER TABLE contacts ADD COLUMN IF NOT EXISTS phones JSONB NOT NULL DEFAULT '[]';
    END IF;
END $$;

UPDATE contacts SET
    emails = CASE
        WHEN email <> '' THEN jsonb_build_array(
            jsonb_build_object('value', email, 'type', jsonb_build_array('INTERNET'))
        )
        ELSE '[]'::jsonb
    END
WHERE emails::text = '[]' AND email IS NOT NULL AND email <> '';

UPDATE contacts SET
    phones = CASE
        WHEN phone IS NOT NULL AND phone <> '' THEN jsonb_build_array(
            jsonb_build_object('value', phone, 'type', jsonb_build_array('CELL', 'VOICE'))
        )
        ELSE '[]'::jsonb
    END
WHERE phones::text = '[]' AND phone IS NOT NULL AND phone <> '';
