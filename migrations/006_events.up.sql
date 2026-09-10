-- CalDAV clients address an event by the resource name in the PUT URL (often
-- not a UUID) and expect to GET the same iCalendar body back at that href.
-- starts_at/ends_at are best-effort for the web UI and may be null for all-day,
-- floating, or recurring events.
CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    title TEXT,
    starts_at TIMESTAMP,
    ends_at TIMESTAMP,
    etag TEXT NOT NULL,
    ics TEXT,
    uid TEXT,
    resource TEXT,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_events_user
ON events(user_id);

CREATE UNIQUE INDEX idx_events_user_resource
ON events (user_id, resource);
