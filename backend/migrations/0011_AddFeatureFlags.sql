CREATE TABLE feature_flags (
    id varchar(22) PRIMARY KEY, app_id varchar(22) NOT NULL REFERENCES apps(id),
    key varchar(255) NOT NULL, value varchar(4000) NOT NULL, environment varchar(50) NOT NULL,
    conditions varchar(4000) NOT NULL, created_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'), modified_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
CREATE INDEX idx_feature_flags_app_key_env ON feature_flags(app_id,key,environment);
