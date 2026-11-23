CREATE TABLE app_salts (
    app_id varchar(22) NOT NULL REFERENCES apps(id), date varchar(10) NOT NULL, salt bytea NOT NULL,
    PRIMARY KEY(app_id, date)
);
