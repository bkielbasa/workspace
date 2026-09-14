-- Guest list and iTIP sequence for meeting invitations. Attendees holds
-- the invitee emails for events organized by the user; sequence tracks
-- REQUEST updates so stale inbound invites can be ignored.
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS attendees TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS sequence INT NOT NULL DEFAULT 0;
