-- Grant the default personal role the workspace capabilities required by the
-- activation bootstrap. Existing compiled RoleEntry values are refreshed in
-- this migration; fresh installations also receive the same grants in 000006.

CREATE FUNCTION iam_workspace_role_varint(input_value bigint)
RETURNS bytea
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    remaining bigint := input_value;
    current_byte integer;
    encoded bytea := ''::bytea;
BEGIN
    IF remaining = 0 THEN
        RETURN decode('00', 'hex');
    END IF;
    WHILE remaining > 0 LOOP
        current_byte := (remaining & 127)::integer;
        remaining := remaining >> 7;
        IF remaining > 0 THEN
            current_byte := current_byte | 128;
        END IF;
        encoded := encoded || decode(lpad(to_hex(current_byte), 2, '0'), 'hex');
    END LOOP;
    RETURN encoded;
END;
$$;

CREATE FUNCTION iam_workspace_role_entry(permission_keys text[])
RETURNS bytea
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    permission_key text;
    permission_bytes bytea;
    encoded bytea := ''::bytea;
BEGIN
    FOREACH permission_key IN ARRAY COALESCE(permission_keys, ARRAY[]::text[]) LOOP
        permission_bytes := convert_to(permission_key, 'UTF8');
        encoded := encoded || decode('0a', 'hex')
                  || iam_workspace_role_varint(octet_length(permission_bytes))
                  || permission_bytes;
    END LOOP;
    RETURN encoded;
END;
$$;

INSERT INTO platform_role_permissions (role_id, permission_id)
SELECT role.id, permission.id
FROM platform_roles AS role
JOIN permissions AS permission
  ON permission.module = 'hierarchy'
 AND permission.object = 'workspace'
 AND permission.behavior IN ('create', 'read', 'delete')
WHERE role.code = 'platform_user'
ON CONFLICT DO NOTHING;

UPDATE platform_roles
SET version = version + 1, updated_at = NOW()
WHERE code = 'platform_user';

WITH compiled AS (
    SELECT assignment.id,
           role.version,
           iam_workspace_role_entry(array_agg(
               user_account.username || ':' || assignment.workspace_id::text || ':'
               || permission.module || ':' || permission.object || ':' || permission.behavior
               ORDER BY permission.module, permission.object, permission.behavior
           )) AS list_perm
    FROM user_role AS assignment
    JOIN users AS user_account ON user_account.id = assignment.user_id
    JOIN platform_roles AS role ON role.id = assignment.role_id
    JOIN platform_role_permissions AS mapping ON mapping.role_id = role.id
    JOIN permissions AS permission ON permission.id = mapping.permission_id
    WHERE role.code = 'platform_user'
    GROUP BY assignment.id, role.version, user_account.username, assignment.workspace_id
)
UPDATE user_role AS assignment
SET role_version = compiled.version,
    list_perm = compiled.list_perm,
    updated_at = NOW()
FROM compiled
WHERE assignment.id = compiled.id;

DROP FUNCTION iam_workspace_role_entry(text[]);
DROP FUNCTION iam_workspace_role_varint(bigint);
