# IAM Refresh Token Flow

## Summary
- route: `POST /api/v1/auth/refresh`
- input: `refresh_token` từ HttpOnly cookie
- success: rotate refresh token, issue access token mới, set lại 2 cookies
- response success: `204 No Content`
- không trả body, không trả token trong JSON

## Flow
1. handler đọc cookie `refresh_token`
2. service hash refresh token raw bằng `SHA-256`
3. repository load `refresh_tokens` theo `token_hash`
4. service check session tồn tại và chưa hết hạn
5. repository load user tối thiểu theo `user_id`
6. service block `pending-active`, `suspended`, `disabled`
7. service ký access JWT mới
8. service sinh refresh token opaque mới
9. repository rotate session trong transaction:
   - xóa row refresh token cũ
   - insert row refresh token mới
10. handler set `access_token` + `refresh_token` cookies mới
11. handler trả `204`

## Error contract
- `401 unauthorized` -> `invalid session`
- `503 service unavailable` -> `authentication temporarily unavailable`
- `500 internal server error`

## Security notes
- raw refresh token không lưu DB
- DB chỉ lưu `refresh_tokens.token_hash`
- access JWT không chứa `status`
- refresh token cũ hết hiệu lực ngay sau khi rotate thành công
