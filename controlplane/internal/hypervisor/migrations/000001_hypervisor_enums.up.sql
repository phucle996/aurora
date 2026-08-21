DO $$
BEGIN
    CREATE TYPE hypervisor_vm_status AS ENUM ('PROVISIONING', 'READY', 'DELETING');
EXCEPTION
    WHEN duplicate_object THEN NULL;
END
$$;

DO $$
BEGIN
    CREATE TYPE hypervisor_image_state AS ENUM (
        'UPLOADING',
        'IMPORTING',
        'AVAILABLE',
        'QUARANTINED',
        'FAILED',
        'DELETING'
    );
EXCEPTION
    WHEN duplicate_object THEN NULL;
END
$$;

DO $$
BEGIN
    CREATE TYPE hypervisor_outbox_status AS ENUM (
        'PENDING',
        'PROCESSING',
        'SUCCEEDED',
        'FAILED',
        'DEAD'
    );
EXCEPTION
    WHEN duplicate_object THEN NULL;
END
$$;
