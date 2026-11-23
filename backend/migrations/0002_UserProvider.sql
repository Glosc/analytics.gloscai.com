CREATE TABLE user_providers (
    provider_name varchar(40) NOT NULL, provider_uid varchar(40) NOT NULL,
    user_id varchar(22) NOT NULL REFERENCES users(id), PRIMARY KEY(provider_name, provider_uid),
    created_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'), modified_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
