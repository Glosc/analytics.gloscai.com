CREATE TABLE apps (
    id varchar(22) PRIMARY KEY, owner_id varchar(22) NOT NULL, name varchar(40) NOT NULL,
    app_key varchar(20) NOT NULL, deleted_at timestamp,
    created_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'), modified_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
CREATE INDEX idx_apps_owner ON apps(owner_id);
CREATE UNIQUE INDEX idx_apps_key ON apps(app_key);
CREATE TABLE users (
    id varchar(22) PRIMARY KEY, name varchar(40) NOT NULL, email varchar(300) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'), modified_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
CREATE UNIQUE INDEX idx_users_email ON users(email);
