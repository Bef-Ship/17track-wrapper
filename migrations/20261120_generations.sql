-- 17TRACK-aligned carrier, register params, tracking-number generations, event coordinates.
--
-- Carriers recycle tracking numbers, so a package row is one *generation* of a number:
-- at most one current row per tracking_number (superseded_at IS NULL); older
-- generations stay queryable as history (hot or archived).

-- Carrier: the 17TRACK carrier key only (0 = auto-detect / unresolved).
ALTER TABLE tracking.package RENAME COLUMN sdk_carrier_key TO carrier;
UPDATE tracking.package SET carrier = 0 WHERE carrier IS NULL;
ALTER TABLE tracking.package
    ALTER COLUMN carrier SET DEFAULT 0,
    ALTER COLUMN carrier SET NOT NULL,
    DROP COLUMN IF EXISTS carrier_code,
    DROP COLUMN IF EXISTS carrier_mode,
    DROP COLUMN IF EXISTS provider_name;

-- Last 17TRACK /register request (lang, order_no, destination_*, …) for retries.
ALTER TABLE tracking.package
    ADD COLUMN IF NOT EXISTS register_params jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS superseded_at timestamptz;

ALTER TABLE tracking.package DROP CONSTRAINT IF EXISTS package_tracking_number_key;
CREATE UNIQUE INDEX IF NOT EXISTS package_current_number_key
    ON tracking.package (tracking_number) WHERE superseded_at IS NULL;
CREATE INDEX IF NOT EXISTS package_number_history_idx
    ON tracking.package (tracking_number, package_id DESC);

-- Archive: inactive packages (not REGISTERING / TRACKING) untouched for the retention
-- window. updated_at is the anchor so restore / supersede restart the clock.
DROP INDEX IF EXISTS tracking.package_due_archive_idx;
CREATE INDEX IF NOT EXISTS package_due_archive_idx
    ON tracking.package (updated_at)
    WHERE is_archived = false AND subscription_status NOT IN ('REGISTERING', 'TRACKING');

-- event.address.coordinates (route GeoJSON is built from stored events).
ALTER TABLE tracking.tracking_event
    ADD COLUMN IF NOT EXISTS latitude  double precision,
    ADD COLUMN IF NOT EXISTS longitude double precision;

COMMENT ON TABLE tracking.package IS
    '包裹聚合：17TRACK 物流快照 + 订阅/归档/退件；运单号可被承运商回收，每行是一代，当前代 superseded_at IS NULL 唯一。';
COMMENT ON COLUMN tracking.package.carrier IS '17TRACK carrier key (0 = auto-detect)';
COMMENT ON COLUMN tracking.package.superseded_at IS 'set when a newer generation of the same tracking_number was registered';
