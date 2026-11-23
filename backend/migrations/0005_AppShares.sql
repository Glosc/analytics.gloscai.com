CREATE TABLE app_shares (
    app_id varchar(22) NOT NULL REFERENCES apps(id), email varchar(300) NOT NULL, PRIMARY KEY(app_id, email),
    created_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'), modified_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
