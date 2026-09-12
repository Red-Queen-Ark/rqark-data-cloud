# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| POST | `/api/auth/register` | `{ "username": str, "email": str, "password": str, "display_name": str }` | 註冊 |
| POST | `/api/auth/login` | form-urlencoded：`username`, `password` | 登入（取 access + refresh token） |
| GET | `/api/auth/me` | — | 取得目前登入用戶 |
| POST | `/api/auth/refresh` | `{ "refresh_token": str }` | 用 refresh token 換新 token |
| POST | `/api/auth/password` | `{ "old_password": str, "new_password": str }` | 修改自己密碼 |
| POST | `/api/auth/forgot-password` | `{ "email": str }` | 發送重設密碼 email |
| POST | `/api/auth/reset-password` | `{ "token": str, "new_password": str }` | 用 email 內 token 重設密碼 |


只有 `/api/auth/me` 需要 `Authorization: Bearer <access_token>`，