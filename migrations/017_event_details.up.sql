-- Structured fields for the web calendar. The raw ICS body remains the
-- CalDAV round-trip payload; these columns power the week view and Encode.
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS location TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
