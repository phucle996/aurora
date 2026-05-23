DROP INDEX IF EXISTS admin_devices_client_device_uidx;
ALTER TABLE admin_devices DROP COLUMN IF EXISTS client_device_id;

DROP INDEX IF EXISTS devices_user_client_device_uidx;
ALTER TABLE devices DROP COLUMN IF EXISTS client_device_id;
