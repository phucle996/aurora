-- iam: add persistent client_device_id columns for device identity (idea v0.3).
-- v1 contract: identify device by (user_id, client_device_id) for users
-- and (admin_id, client_device_id) for admins. DB id (devices.id, admin_devices.id)
-- remains server-internal and must not be exposed to clients.

ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS client_device_id varchar(128) NULL;

CREATE UNIQUE INDEX IF NOT EXISTS devices_user_client_device_uidx
    ON devices(user_id, client_device_id)
    WHERE client_device_id IS NOT NULL;

COMMENT ON COLUMN devices.client_device_id IS
    'Persistent opaque device identifier supplied by the client (X-Client-Device-Id) or bootstrapped by server. Identity key for repeat logins; never exposes devices.id.';

ALTER TABLE admin_devices
    ADD COLUMN IF NOT EXISTS client_device_id varchar(128) NULL;

CREATE UNIQUE INDEX IF NOT EXISTS admin_devices_client_device_uidx
    ON admin_devices(client_device_id)
    WHERE client_device_id IS NOT NULL;

COMMENT ON COLUMN admin_devices.client_device_id IS
    'Persistent opaque admin client device identifier from header X-Client-Device-Id or server bootstrap. Identity key for repeat admin logins.';
