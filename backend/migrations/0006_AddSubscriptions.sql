CREATE TABLE subscriptions (
    id bigint PRIMARY KEY, owner_id varchar(22) NOT NULL, customer_id bigint NOT NULL,
    product_id bigint NOT NULL, variant_id bigint NOT NULL, status text NOT NULL, ends_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC'), modified_at timestamptz NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')
);
CREATE INDEX idx_subscriptions_owner ON subscriptions(owner_id);
