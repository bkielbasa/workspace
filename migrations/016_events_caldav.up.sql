-- CalDAV clients (iOS, DAVx5) address an event by the resource name in the PUT
-- URL (often not a UUID) and expect to GET it back byte-for-byte at that same
-- href. Store the raw iCalendar body, the client UID, and the resource name;
-- let times be optional so all-day / floating / recurring events (which we do
-- not model) still round trip. starts_at/ends_at remain only as a best-effort
-- index for the web UI.
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS ics TEXT,
    ADD COLUMN IF NOT EXISTS uid TEXT,
    ADD COLUMN IF NOT EXISTS resource TEXT;

ALTER TABLE events ALTER COLUMN starts_at DROP NOT NULL;
ALTER TABLE events ALTER COLUMN ends_at DROP NOT NULL;

-- Web-created rows use their uuid as the resource name; back-fill so every row
-- has one before the uniqueness index is added.
UPDATE events SET resource = id::text WHERE resource IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_events_user_resource
    ON events (user_id, resource);
