-- [COMMENT]: Platform identity được insert một lần; migration không mutate template lifecycle đã tồn tại.
INSERT INTO mail_templates (
    id, workspace_id, scope, name, current_version, template_revision, status, created_at, updated_at
)
VALUES (
    'platform/verify_account', NULL, 'platform', 'Verify Account', 1, 1, 'active', now(), now()
)
ON CONFLICT (id) DO NOTHING;

-- [COMMENT]: Version 1 là immutable. Hash là SHA-256 của canonical seed content do release tạo sẵn.
INSERT INTO mail_template_versions (
    template_id,
    version,
    subject_template,
    text_template,
    html_template,
    variable_schema_json,
    content_sha256,
    created_at
)
VALUES (
    'platform/verify_account',
    1,
    'Kích hoạt tài khoản của bạn',
    '',
    '<html><body><h1>Xin chào {{fullname}},</h1><p>Vui lòng mở trang Aurora để xác nhận kích hoạt: <a href="https://cloud.aurora.local/activate#user_id={{user_id}}&event_id={{event_id}}&token={{verify_token}}">Kích hoạt tài khoản</a></p></body></html>',
    '{"type":"object","required":["fullname","user_id","event_id","verify_token"],"properties":{"fullname":{"type":"string"},"user_id":{"type":"string"},"event_id":{"type":"string"},"verify_token":{"type":"string"}},"additionalProperties":false}'::jsonb,
    decode('3d4fff46e64b33228e3fa697f3804e3c0014d57d807b2150f47d8ca52ca9d944', 'hex'),
    now()
)
ON CONFLICT (template_id, version) DO NOTHING;
