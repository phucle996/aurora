-- [COMMENT]: Rollback migration là đường duy nhất được phép bypass COW trigger.
SELECT set_config('mail.allow_template_version_mutation', 'on', true);

DELETE FROM system_mail_template_versions WHERE template_id = 'system/verify_account' AND version = 1;

DELETE FROM system_mail_templates WHERE id = 'system/verify_account';
