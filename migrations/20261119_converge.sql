-- Converge databases created from the pre-squash, tenant-scoped 20261102 DDL
-- onto the current schema. Every statement is a no-op on fresh installs.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM tracking.package
        GROUP BY tracking_number HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'tracking.package has duplicate tracking_number rows (multi-tenant data); dedupe before UNIQUE (tracking_number)';
    END IF;
END $$;

-- Dropping tenant_id also drops UNIQUE (tenant_id, tracking_number) and the package_tenant_* indexes.
ALTER TABLE tracking.package
    DROP COLUMN IF EXISTS tenant_id,
    DROP COLUMN IF EXISTS tenant_webhook_url,
    DROP COLUMN IF EXISTS sdk_register_id,
    DROP COLUMN IF EXISTS last_callback_seq,
    DROP COLUMN IF EXISTS registered_at,
    DROP COLUMN IF EXISTS label_url,
    DROP COLUMN IF EXISTS dim_length,
    DROP COLUMN IF EXISTS dim_width,
    DROP COLUMN IF EXISTS dim_height;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'tracking.package'::regclass
          AND conname = 'package_tracking_number_key'
    ) THEN
        ALTER TABLE tracking.package
            ADD CONSTRAINT package_tracking_number_key UNIQUE (tracking_number);
    END IF;
END $$;

-- Covered by the primary key / UNIQUE (tracking_number) / package_subscription_id_idx.
DROP INDEX IF EXISTS tracking.package_id_idx;
DROP INDEX IF EXISTS tracking.package_sub_status_idx;
DROP INDEX IF EXISTS tracking.package_number_key_idx;

CREATE INDEX IF NOT EXISTS package_status_idx
    ON tracking.package (status, package_id DESC);
CREATE INDEX IF NOT EXISTS package_return_idx
    ON tracking.package (has_return, status);

COMMENT ON COLUMN tracking.package.last_callback_at IS
    'last 17TRACK data received (webhook or poll); SyncActive skips recently heard packages';

ALTER TABLE tracking.tracking_event
    DROP COLUMN IF EXISTS tracking_number,
    DROP COLUMN IF EXISTS raw_json,
    DROP COLUMN IF EXISTS carrier_code,
    DROP COLUMN IF EXISTS status_code;
