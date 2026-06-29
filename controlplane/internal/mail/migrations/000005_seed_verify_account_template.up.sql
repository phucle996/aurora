-- [COMMENT]: Seed email template cho luồng kích hoạt tài khoản (Verify Account) scope Platform vào bảng mail_templates
INSERT INTO mail_templates (id, name, subject, body, created_at, updated_at)
VALUES (
    'platform/verify_account',
    'Verify Account',
    'Kích hoạt tài khoản của bạn',
    '<html><body><h1>Xin chào {{fullname}},</h1><p>Vui lòng click vào link sau để kích hoạt tài khoản: <a href="https://cloud.aurora.local/api/v1/auth/verify?user_id={{user_id}}&token={{verify_token}}">Kích hoạt tài khoản</a></p></body></html>',
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
) ON CONFLICT (id) DO UPDATE SET
    subject = EXCLUDED.subject,
    body = EXCLUDED.body,
    updated_at = CURRENT_TIMESTAMP;
