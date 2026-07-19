-- [COMMENT]: Drop theo thứ tự ngược dependency của migration up.
DROP TYPE IF EXISTS mail_result_inbox_status;
DROP TYPE IF EXISTS mail_execution_status;
DROP TYPE IF EXISTS mail_template_status;
DROP TYPE IF EXISTS mail_template_scope;
DROP TYPE IF EXISTS mail_consumer_runtime_state;
DROP TYPE IF EXISTS mail_consumer_desired_state;
DROP TYPE IF EXISTS mail_source_type;
