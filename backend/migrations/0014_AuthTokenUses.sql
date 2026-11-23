CREATE TABLE auth_token_uses (token_id varchar(22) PRIMARY KEY, expires_at timestamp NOT NULL, used_at timestamp NOT NULL);
CREATE INDEX idx_auth_token_uses_expires_at ON auth_token_uses(expires_at);
