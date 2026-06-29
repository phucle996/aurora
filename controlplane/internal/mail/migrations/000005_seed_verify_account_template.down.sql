-- [COMMENT]: Rollback: Xóa email template kích hoạt tài khoản
DELETE FROM mail_templates WHERE id = 'platform/verify_account';
