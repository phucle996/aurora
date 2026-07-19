-- [COMMENT]: Drop theo thứ tự ngược foreign-key dependency.
DROP TRIGGER IF EXISTS trg_mail_template_versions_immutable ON mail_template_versions;
DROP FUNCTION IF EXISTS reject_mail_template_version_mutation();
DROP TABLE IF EXISTS mail_delivery_attempts;
DROP TABLE IF EXISTS mail_submissions;
DROP TABLE IF EXISTS mail_result_inbox;
DROP TABLE IF EXISTS mail_consumer_runtime_reports;
DROP TABLE IF EXISTS mail_consumers;
DROP TABLE IF EXISTS mail_template_versions;
DROP TABLE IF EXISTS mail_templates;
