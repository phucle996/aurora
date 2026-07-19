-- [COMMENT]: Rollback migration là đường duy nhất được phép bypass COW trigger.
SELECT set_config('mail.allow_template_version_mutation', 'on', true);

DELETE FROM mail_template_versions
WHERE template_id = 'platform/verify_account' AND version = 1;

DELETE FROM mail_templates
WHERE id = 'platform/verify_account';
