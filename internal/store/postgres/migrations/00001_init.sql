-- +goose Up
CREATE TABLE package (
    package_id BIGSERIAL PRIMARY KEY,
    tenant_id VARCHAR(64) NOT NULL DEFAULT 'default',
    tracking_number VARCHAR(128) NOT NULL,
    carrier_code VARCHAR(64) NOT NULL,
    provider_name VARCHAR(64) NOT NULL DEFAULT '',
    external_ref VARCHAR(256) NOT NULL DEFAULT '',
    label_url TEXT NOT NULL DEFAULT '',
    weight NUMERIC,
    dim_length NUMERIC,
    dim_width NUMERIC,
    dim_height NUMERIC,

    status VARCHAR(32) NOT NULL DEFAULT 'registered',
    sub_status VARCHAR(64) NOT NULL DEFAULT '',
    sub_status_desc TEXT NOT NULL DEFAULT '',

    origin_country VARCHAR(8) NOT NULL DEFAULT '',
    origin_state VARCHAR(128) NOT NULL DEFAULT '',
    origin_city VARCHAR(128) NOT NULL DEFAULT '',
    origin_postal_code VARCHAR(32) NOT NULL DEFAULT '',
    destination_country VARCHAR(8) NOT NULL DEFAULT '',
    destination_state VARCHAR(128) NOT NULL DEFAULT '',
    destination_city VARCHAR(128) NOT NULL DEFAULT '',
    destination_postal_code VARCHAR(32) NOT NULL DEFAULT '',
    service_type VARCHAR(128) NOT NULL DEFAULT '',
    pieces INT,
    reference_number VARCHAR(128) NOT NULL DEFAULT '',
    last_mile_tracking_number VARCHAR(128) NOT NULL DEFAULT '',
    last_mile_carrier_code VARCHAR(64) NOT NULL DEFAULT '',
    estimated_delivery_from TIMESTAMPTZ,
    estimated_delivery_to TIMESTAMPTZ,

    milestones JSONB NOT NULL DEFAULT '{}',
    picked_up_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,

    latest_event_time TIMESTAMPTZ,
    latest_event_desc TEXT NOT NULL DEFAULT '',
    latest_event_location TEXT NOT NULL DEFAULT '',

    is_archived BOOLEAN NOT NULL DEFAULT false,
    archive_key TEXT NOT NULL DEFAULT '',
    latest_update_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, tracking_number)
);
CREATE INDEX idx_package_tenant_id ON package(tenant_id, package_id DESC);
CREATE INDEX idx_package_tenant_status ON package(tenant_id, status, package_id DESC);
CREATE INDEX idx_package_due_archive ON package(delivered_at)
    WHERE is_archived = false AND delivered_at IS NOT NULL;

CREATE TABLE tracking_event (
    event_id BIGSERIAL PRIMARY KEY,
    package_id BIGINT NOT NULL REFERENCES package(package_id),
    tracking_number VARCHAR(128) NOT NULL,
    event_time TIMESTAMPTZ NOT NULL,
    carrier_code VARCHAR(64) NOT NULL DEFAULT '',
    stage VARCHAR(32) NOT NULL DEFAULT '',
    sub_status VARCHAR(64) NOT NULL DEFAULT '',
    status_code VARCHAR(64) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    location TEXT NOT NULL DEFAULT '',
    country VARCHAR(8) NOT NULL DEFAULT '',
    state VARCHAR(128) NOT NULL DEFAULT '',
    city VARCHAR(128) NOT NULL DEFAULT '',
    postal_code VARCHAR(32) NOT NULL DEFAULT '',
    raw_json JSONB,
    event_fingerprint VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (package_id, event_fingerprint)
);

-- +goose Down
DROP TABLE tracking_event;
DROP TABLE package;
