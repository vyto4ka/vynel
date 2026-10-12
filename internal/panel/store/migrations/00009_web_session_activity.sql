-- Web sessions end on inactivity, on a changed IP and when the page is closed; the bot's sign-in
-- message is deleted when its session ends (docs/STEALTH.md §3.3).

-- +goose Up
ALTER TABLE web_sessions ADD COLUMN last_active_at INTEGER NOT NULL DEFAULT 0; -- last user action (clicks, keys), not polling
ALTER TABLE web_sessions ADD COLUMN closing_at INTEGER;                        -- the page said it was closing
ALTER TABLE web_sessions ADD COLUMN end_reason TEXT NOT NULL DEFAULT '';       -- logout | idle | ip | closed | ...

-- Messages the bot sent about a sign-in, deleted when the session ends.
CREATE TABLE web_session_notices (
    session_id TEXT    NOT NULL,
    chat_id    INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    PRIMARY KEY (session_id, chat_id, message_id)
);

-- +goose Down
DROP TABLE web_session_notices;
ALTER TABLE web_sessions DROP COLUMN end_reason;
ALTER TABLE web_sessions DROP COLUMN closing_at;
ALTER TABLE web_sessions DROP COLUMN last_active_at;
