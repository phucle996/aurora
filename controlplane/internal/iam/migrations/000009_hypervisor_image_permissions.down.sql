DELETE FROM role_permissions
WHERE permission_id IN (
    SELECT id
    FROM permissions
    WHERE module = 'hypervisor'
      AND object = 'image'
      AND behavior IN ('read', 'create', 'publish', 'delete')
);

DELETE FROM permissions
WHERE module = 'hypervisor'
  AND object = 'image'
  AND behavior IN ('read', 'create', 'publish', 'delete');

