CREATE TABLE blobs (
    path varchar(80) PRIMARY KEY, content bytea NOT NULL, content_type varchar(80) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'), modified_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
ALTER TABLE apps ADD COLUMN icon_path varchar(80);
