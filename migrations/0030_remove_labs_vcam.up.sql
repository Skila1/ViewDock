-- The Labs virtual camera broadcaster was removed. Its stored output URL
-- usually embedded a stream key, so the leftover settings are deleted.
DELETE FROM server_settings WHERE key IN ('labs.vcam.ack', 'labs.vcam.config', 'labs.vcam.output_url');
