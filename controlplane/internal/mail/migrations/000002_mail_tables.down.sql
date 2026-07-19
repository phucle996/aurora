-- [COMMENT]: Drop theo thứ tự ngược foreign-key dependency.
DROP TRIGGER IF EXISTS trg_system_mail_template_versions_immutable ON system_mail_template_versions;
DROP TRIGGER IF EXISTS trg_tenant_mail_template_versions_immutable ON tenant_mail_template_versions;
DROP TRIGGER IF EXISTS trg_personal_mail_template_versions_immutable ON personal_mail_template_versions;
DROP FUNCTION IF EXISTS reject_mail_template_version_mutation();
DROP TABLE IF EXISTS mail_delivery_attempts;
DROP TABLE IF EXISTS mail_submissions;
DROP TABLE IF EXISTS mail_result_inbox;
DROP TABLE IF EXISTS mail_consumer_runtime_reports;
DROP TABLE IF EXISTS mail_consumers;
DROP TABLE IF EXISTS system_mail_template_versions;
DROP TABLE IF EXISTS system_mail_templates;
DROP TABLE IF EXISTS tenant_mail_template_versions;
DROP TABLE IF EXISTS tenant_mail_templates;
DROP TABLE IF EXISTS personal_mail_template_versions;
DROP TABLE IF EXISTS personal_mail_templates;
