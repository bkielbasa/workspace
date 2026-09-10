-- Structured fields for the web calendar. The raw ICS body remains the
-- CalDAV round-trip payload; these columns power the week view and Encode.
ALTER TABLE events
    ADD COLUMN location TEXT NOT NULL DEFAULT '',
    ADD COLUMN description TEXT NOT NULL DEFAULT '';
