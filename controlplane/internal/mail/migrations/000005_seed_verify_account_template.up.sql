-- [COMMENT]: System identity dành riêng IAM; không gán ownership Personal/Tenant giả.
INSERT INTO system_mail_templates (id, name, current_version, updated_at)
VALUES (
    'system/verify_account', 'Verify Account', 1, now()
)
ON CONFLICT (id) DO NOTHING;

-- [COMMENT]: Version 1 immutable; hash được tính ngay từ subject + NUL + HTML để không drift khi sửa seed.
INSERT INTO system_mail_template_versions (
    template_id,
    version,
    subject_template,
    html_template,
    content_sha256,
    created_at
)
VALUES (
    'system/verify_account',
    1,
    'Kích hoạt tài khoản của bạn',
    '<html><body><h1>Xin chào {{fullname}},</h1><p>Vui lòng mở trang Aurora để xác nhận kích hoạt: <a href="https://cloud.aurora.local/activate#user_id={{user_id}}&event_id={{event_id}}&token={{verify_token}}">Kích hoạt tài khoản</a></p></body></html>',
    decode('7a78142c15c89e51eaf333e6fa4290169dbe554fc6957b9ed48f76e8316a40a8', 'hex'),
    now()
)
ON CONFLICT (template_id, version) DO NOTHING;
