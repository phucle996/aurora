-- [COMMENT]: COW là invariant tại database.
CREATE OR REPLACE FUNCTION reject_mail_consumer_delete_unless_deleting()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.desired_state <> 'deleting' THEN
        RAISE EXCEPTION 'consumer must be deleting before physical record deletion' USING ERRCODE = '55000';
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_personal_mail_consumer_delete_state ON personal_mail_consumers;
CREATE TRIGGER trg_personal_mail_consumer_delete_state
BEFORE DELETE ON personal_mail_consumers FOR EACH ROW
EXECUTE FUNCTION reject_mail_consumer_delete_unless_deleting();

DROP TRIGGER IF EXISTS trg_tenant_mail_consumer_delete_state ON tenant_mail_consumers;
CREATE TRIGGER trg_tenant_mail_consumer_delete_state
BEFORE DELETE ON tenant_mail_consumers FOR EACH ROW
EXECUTE FUNCTION reject_mail_consumer_delete_unless_deleting();
CREATE OR REPLACE FUNCTION reject_mail_template_version_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF current_setting('mail.allow_template_version_mutation', true) = 'on' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION '% is immutable; publish a new version', TG_TABLE_NAME
        USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;

DO $$
BEGIN
    -- [COMMENT]: Trigger name không unique toàn database; bind check vào relation của search_path hiện tại.
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgname = 'trg_personal_mail_template_versions_immutable'
          AND tgrelid = 'personal_mail_template_versions'::regclass
          AND NOT tgisinternal
    ) THEN
        CREATE TRIGGER trg_personal_mail_template_versions_immutable BEFORE UPDATE OR DELETE ON personal_mail_template_versions FOR EACH ROW EXECUTE FUNCTION reject_mail_template_version_mutation();
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgname = 'trg_tenant_mail_template_versions_immutable'
          AND tgrelid = 'tenant_mail_template_versions'::regclass
          AND NOT tgisinternal
    ) THEN
        CREATE TRIGGER trg_tenant_mail_template_versions_immutable BEFORE UPDATE OR DELETE ON tenant_mail_template_versions FOR EACH ROW EXECUTE FUNCTION reject_mail_template_version_mutation();
    END IF;
END $$;
