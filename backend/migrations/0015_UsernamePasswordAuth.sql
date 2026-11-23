ALTER TABLE users ADD COLUMN username varchar(40), ADD COLUMN password_hash varchar(500), ADD COLUMN role varchar(20) NOT NULL DEFAULT 'user';
UPDATE users SET username = left(coalesce(nullif(regexp_replace(split_part(lower(email), '@', 1), '[^a-z0-9._-]', '', 'g'), ''), 'user'), 17)
                            || '_' || lower(left(id, 22)) WHERE username IS NULL;
ALTER TABLE users ALTER COLUMN username SET NOT NULL, ALTER COLUMN email DROP NOT NULL;
CREATE UNIQUE INDEX idx_users_username ON users(username);
CREATE UNIQUE INDEX idx_users_single_admin ON users(role) WHERE role = 'admin';
DROP TABLE auth_token_uses;
