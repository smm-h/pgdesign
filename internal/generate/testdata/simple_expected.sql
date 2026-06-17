CREATE SCHEMA shop;

CREATE EXTENSION pgcrypto;

CREATE TABLE shop.customers (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    email text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_customers PRIMARY KEY (id)
);

CREATE TABLE shop.orders (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    customer_id uuid NOT NULL,
    total bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_orders PRIMARY KEY (id)
);

ALTER TABLE shop.orders ADD CONSTRAINT fk_orders_customers FOREIGN KEY (customer_id) REFERENCES shop.customers (id) ON DELETE CASCADE;

ALTER TABLE shop.customers ADD CONSTRAINT uq_customers_email UNIQUE (email);

ALTER TABLE shop.customers ADD CONSTRAINT chk_customers_email CHECK (email ~ '^[^@]+@[^@]+\.[^@]+$');
ALTER TABLE shop.customers ADD CONSTRAINT chk_customers_name CHECK (LENGTH(name) <= 255);
ALTER TABLE shop.orders ADD CONSTRAINT ck_orders_positive_total CHECK (total >= 0);

CREATE INDEX idx_orders_status ON shop.orders (customer_id);

COMMENT ON TABLE shop.customers IS 'Registered customers';
COMMENT ON COLUMN shop.customers.name IS 'Full name';
COMMENT ON TABLE shop.orders IS 'Customer orders';
