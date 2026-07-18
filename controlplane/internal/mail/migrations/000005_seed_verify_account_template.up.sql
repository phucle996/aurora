-- [COMMENT]: Seed email template cho luồng kích hoạt tài khoản (Verify Account) scope Platform vào bảng mail_templates
INSERT INTO mail_templates (id, name, subject, body, created_at, updated_at)
VALUES (
    'platform/verify_account',
    'Verify Account',
    'Kích hoạt tài khoản của bạn',
    '<html><body><h1>Xin chào {{fullname}},</h1><p>Vui lòng mở trang Aurora để xác nhận kích hoạt: <a href="https://cloud.aurora.local/activate#user_id={{user_id}}&event_id={{event_id}}&token={{verify_token}}">Kích hoạt tài khoản</a></p></body></html>',
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
) ON CONFLICT (id) DO UPDATE SET
    subject = EXCLUDED.subject,
    body = EXCLUDED.body,
    updated_at = CURRENT_TIMESTAMP;
