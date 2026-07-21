-- [COMMENT]: Drop theo thứ tự ngược dependency của migration up.
DROP TYPE IF EXISTS mail_consumer_runtime_state;
DROP TYPE IF EXISTS mail_consumer_desired_state;
DROP TYPE IF EXISTS mail_source_type;
