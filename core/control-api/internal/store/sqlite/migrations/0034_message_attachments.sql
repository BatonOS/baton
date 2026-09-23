










CREATE TABLE IF NOT EXISTS message_attachments (
    message_id   TEXT NOT NULL,


    idx          INTEGER NOT NULL,
    name         TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size         INTEGER NOT NULL,



    sha256       TEXT NOT NULL,

    PRIMARY KEY (message_id, idx),
    FOREIGN KEY (message_id) REFERENCES messages(message_id) ON DELETE CASCADE
);





CREATE INDEX IF NOT EXISTS idx_message_attachments_sha256
    ON message_attachments(sha256);
