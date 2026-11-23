CREATE TABLE app_requests (
    app_id varchar(22) NOT NULL REFERENCES apps(id), current_owner_id varchar(22) NOT NULL REFERENCES users(id),
    target_user_email varchar(300) NOT NULL, purpose varchar(20) NOT NULL, requested_at timestamp NOT NULL,
    completed_at timestamp, status varchar(20) NOT NULL DEFAULT 'pending'
);
CREATE INDEX idx_app_requests_app_id ON app_requests(app_id);
CREATE INDEX idx_app_requests_target_user_email ON app_requests(target_user_email);
CREATE INDEX idx_app_requests_purpose ON app_requests(purpose);
