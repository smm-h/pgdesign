CREATE SCHEMA shop;

CREATE EXTENSION pgcrypto;

CREATE DOMAIN shop.short_text AS text CHECK (LENGTH(VALUE) <= 255);
CREATE DOMAIN shop.email AS text CHECK (VALUE ~ '^[^@]+@[^@]+\.[^@]+$');

CREATE TABLE shop.customers (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name short_text NOT NULL,
    email email NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_customers PRIMARY KEY (id)
);

CREATE TABLE shop.orders (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    customer_id uuid NOT NULL,
    total int8 NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_orders PRIMARY KEY (id)
);

ALTER TABLE shop.orders ADD CONSTRAINT fk_orders_customers FOREIGN KEY (customer_id) REFERENCES shop.customers (id) ON DELETE CASCADE;

ALTER TABLE shop.customers ADD CONSTRAINT uq_customers_email UNIQUE (email);

ALTER TABLE shop.orders ADD CONSTRAINT ck_orders_positive_total CHECK (total >= 0);

CREATE INDEX idx_orders_status ON shop.orders (customer_id);

COMMENT ON TABLE shop.customers IS 'Registered customers';
COMMENT ON COLUMN shop.customers.name IS 'Full name';
COMMENT ON TABLE shop.orders IS 'Customer orders';
