INSERT INTO permissions (id, module, object, behavior, description)
VALUES
    (gen_random_uuid(), 'hypervisor', 'vm', 'read', 'Read personal virtual machines'),
    (gen_random_uuid(), 'hypervisor', 'vm', 'create', 'Create personal virtual machines')
ON CONFLICT (module, object, behavior) DO UPDATE
SET description = EXCLUDED.description,
    updated_at = now();

INSERT INTO role_permissions (role_id, permission_id)
SELECT role.id, permission.id
FROM roles role
CROSS JOIN permissions permission
WHERE role.code IN ('platform_root', 'platform_admin', 'platform_user')
  AND permission.module = 'hypervisor'
  AND permission.object = 'vm'
  AND permission.behavior IN ('read', 'create')
ON CONFLICT (role_id, permission_id) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT role.id, permission.id
FROM roles role
CROSS JOIN permissions permission
WHERE role.code = 'platform_support_operator'
  AND permission.module = 'hypervisor'
  AND permission.object = 'vm'
  AND permission.behavior = 'read'
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- Each repeated RoleEntry string is protobuf field 1. IAM usernames are capped
-- at 64 bytes, so these five-level keys stay below the one-byte varint limit.
UPDATE user_role assignment
SET list_perm = assignment.list_perm
    || decode(
        '0a' || lpad(to_hex(octet_length(convert_to(
            assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:vm:read',
            'UTF8'
        ))), 2, '0'),
        'hex'
    )
    || convert_to(
        assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:vm:read',
        'UTF8'
    )
FROM roles role
WHERE assignment.role_id = role.id
  AND role.code = 'platform_user'
  AND position(
      convert_to(
          assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:vm:read',
          'UTF8'
      ) IN assignment.list_perm
  ) = 0;

UPDATE user_role assignment
SET list_perm = assignment.list_perm
    || decode(
        '0a' || lpad(to_hex(octet_length(convert_to(
            assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:vm:create',
            'UTF8'
        ))), 2, '0'),
        'hex'
    )
    || convert_to(
        assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:vm:create',
        'UTF8'
    )
FROM roles role
WHERE assignment.role_id = role.id
  AND role.code = 'platform_user'
  AND position(
      convert_to(
          assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:vm:create',
          'UTF8'
      ) IN assignment.list_perm
  ) = 0;

UPDATE user_role assignment
SET list_perm = assignment.list_perm
    || decode(
        '0a' || lpad(to_hex(octet_length(convert_to(
            assignment.username || ':*:hypervisor:vm:read',
            'UTF8'
        ))), 2, '0'),
        'hex'
    )
    || convert_to(assignment.username || ':*:hypervisor:vm:read', 'UTF8')
FROM roles role
WHERE assignment.role_id = role.id
  AND role.code IN ('platform_root', 'platform_admin', 'platform_support_operator')
  AND position(
      convert_to(assignment.username || ':*:hypervisor:vm:read', 'UTF8')
      IN assignment.list_perm
  ) = 0;

UPDATE user_role assignment
SET list_perm = assignment.list_perm
    || decode(
        '0a' || lpad(to_hex(octet_length(convert_to(
            assignment.username || ':*:hypervisor:vm:create',
            'UTF8'
        ))), 2, '0'),
        'hex'
    )
    || convert_to(assignment.username || ':*:hypervisor:vm:create', 'UTF8')
FROM roles role
WHERE assignment.role_id = role.id
  AND role.code IN ('platform_root', 'platform_admin')
  AND position(
      convert_to(assignment.username || ':*:hypervisor:vm:create', 'UTF8')
      IN assignment.list_perm
  ) = 0;
