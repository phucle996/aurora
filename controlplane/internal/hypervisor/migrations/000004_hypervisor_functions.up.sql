CREATE OR REPLACE FUNCTION hypervisor_touch_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at := NOW();
    RETURN NEW;
END
$$;

CREATE OR REPLACE FUNCTION hypervisor_require_vm_deleting_before_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status <> 'DELETING' THEN
        RAISE EXCEPTION 'personal VM % cannot be deleted from status %', OLD.id, OLD.status
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END
$$;

CREATE OR REPLACE FUNCTION hypervisor_require_image_deleting_before_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.state <> 'DELETING' THEN
        RAISE EXCEPTION 'hypervisor image % cannot be deleted from state %', OLD.id, OLD.state
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END
$$;
