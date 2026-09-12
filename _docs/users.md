# Table

## users
| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `username` | string(150) | unique, index |
| `email` | string(255) | unique, index |
| `hashed_password` | string(255) | bcrypt hash |
| `display_name` | string(255) | |
| `is_active` | boolean | default true |
| `is_staff` | boolean | default false |
| `is_superuser` | boolean | default false |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/users` | — | 列出全部用戶 |
| GET | `/api/users/{uuid}` | — | 取得單一用戶 |
| POST | `/api/users` | `{ "username": str, "email": str, "password": str, "display_name": str }` | 建立用戶（需 superuser） |
| PUT | `/api/users/{uuid}` | `{ "username"?, "email"?, "password"?, "display_name"?, "is_active"?, "is_staff"?, "is_superuser"? }` | 更新用戶（需 superuser） |
| DELETE | `/api/users/{uuid}` | — | 刪除用戶（需 superuser） |

全部需要 `Authorization: Bearer <access_token>`。