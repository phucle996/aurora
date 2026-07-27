INSERT INTO permissions (id, module, object, behavior, description)
VALUES
    (gen_random_uuid(), 'hypervisor', 'image', 'read', 'Read Zone virtual machine image catalog'),
    (gen_random_uuid(), 'hypervisor', 'image', 'create', 'Register Zone virtual machine image upload'),
    (gen_random_uuid(), 'hypervisor', 'image', 'publish', 'Import and publish Zone virtual machine image'),
    (gen_random_uuid(), 'hypervisor', 'image', 'delete', 'Delete Zone virtual machine image')
ON CONFLICT (module, object, behavior) DO UPDATE
SET description = EXCLUDED.description,
    updated_at = now();

INSERT INTO role_permissions (role_id, permission_id)
SELECT role.id, permission.id
FROM roles role
CROSS JOIN permissions permission
WHERE role.code IN ('platform_root', 'platform_admin')
  AND permission.module = 'hypervisor'
  AND permission.object = 'image'
  AND permission.behavior IN ('read', 'create', 'publish', 'delete')
ON CONFLICT (role_id, permission_id) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT role.id, permission.id
FROM roles role
CROSS JOIN permissions permission
WHERE role.code IN ('platform_user', 'platform_support_operator')
  AND permission.module = 'hypervisor'
  AND permission.object = 'image'
  AND permission.behavior = 'read'
ON CONFLICT (role_id, permission_id) DO NOTHING;

UPDATE user_role assignment
SET list_perm = assignment.list_perm
    || decode(
        '0a' || lpad(to_hex(octet_length(convert_to(
            assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:image:read',
            'UTF8'
        ))), 2, '0'),
        'hex'
    )
    || convert_to(
        assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:image:read',
        'UTF8'
    )
FROM roles role
WHERE assignment.role_id = role.id
  AND role.code = 'platform_user'
  AND position(
      convert_to(
          assignment.username || ':' || assignment.workspace_id::text || ':hypervisor:image:read',
          'UTF8'
      ) IN assignment.list_perm
  ) = 0;

UPDATE user_role assignment
SET list_perm = assignment.list_perm
    || decode(
        '0a' || lpad(to_hex(octet_length(convert_to(
            assignment.username || ':*:hypervisor:image:read',
            'UTF8'
        ))), 2, '0'),
        'hex'
    )
    || convert_to(assignment.username || ':*:hypervisor:image:read', 'UTF8')
FROM roles role
WHERE assignment.role_id = role.id
  AND role.code IN ('platform_root', 'platform_admin', 'platform_support_operator')
  AND position(
      convert_to(assignment.username || ':*:hypervisor:image:read', 'UTF8')
      IN assignment.list_perm
  ) = 0;

UPDATE user_role assignment
SET list_perm = assignment.list_perm
    || decode(
        '0a' || lpad(to_hex(octet_length(convert_to(
            assignment.username || ':*:hypervisor:image:' || behavior.name,
            'UTF8'
        ))), 2, '0'),
        'hex'
    )
    || convert_to(
        assignment.username || ':*:hypervisor:image:' || behavior.name,
        'UTF8'
    )
FROM roles role
CROSS JOIN (VALUES ('create'), ('publish'), ('delete')) AS behavior(name)
WHERE assignment.role_id = role.id
  AND role.code IN ('platform_root', 'platform_admin')
  AND position(
      convert_to(
          assignment.username || ':*:hypervisor:image:' || behavior.name,
          'UTF8'
      ) IN assignment.list_perm
  ) = 0;

