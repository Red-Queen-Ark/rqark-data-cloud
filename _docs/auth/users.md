# Table
## users

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `username` | string(150) | unique, index |
| `email` | string(255) | unique, index |
| `hashed_password` | string(255) | bcrypt |
| `display_name` | string(255) | |
| `is_active` | boolean | default true |
| `is_staff` | boolean | default false，有意保留（暫時未 gate 任何權限） |
| `is_superuser` | boolean | default false |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

# API
| Method | Path | Request body | 權限 | 說明 |
|---|---|---|---|---|
| GET | `/api/users` | — | superuser | 支援 `?limit=&offset=&is_active=`，`?q=` 搜 username / email / display_name |
| POST | `/api/users` | `{ "username", "email", "password", "display_name", "is_superuser"? }` | superuser | 回 `201` + `Location`。用於Superuser幫手註冊用戶 |
| GET | `/api/users/me` | — | 自己 | |
| PATCH | `/api/users/me` | `{ "username"?, "email"?, "display_name"? }` | 自己 | 帶 `is_superuser` 回 `403`，見規則 |
| PUT | `/api/users/me/password` | `{ "old_password", "new_password" }` | 自己 | 改密碼，唔登出其他裝置 |
| GET | `/api/users/{uuid}` | — | superuser | |
| PATCH | `/api/users/{uuid}` | `{ "username"?, "email"?, "display_name"?, "is_superuser"?, "is_staff"?, "is_active"? }`，全部 optional，**不含 `password`**；`is_active` 可 `false`（停用）可 `true`（還原） | superuser | 局部更新；唯一改得到 `is_superuser` 嘅路 |
| PUT | `/api/users/{uuid}/password` | `{ "new_password" }` | superuser | 重設他人密碼，唔登出對方 |
| DELETE | `/api/users/{uuid}` | — | superuser | 停用（`is_active=false`），回 `204` |

全部需要 `Authorization: Bearer <access_token>`。

## 規則

- `/api/users/me` 同 `/api/users/{uuid}` 回同一個 DTO，唔養兩份 serializer。
- `PATCH` 只改有帶嘅欄位。
- 密碼一律經 `/password` 子資源，唔經 `PATCH`。
- `POST /api/auth/register` 同 `POST /api/users` 建同一樣嘅嘢，validation 共用同一段 code。
- **改 `is_superuser` 只有一條路**：superuser 做 `PATCH /api/users/{uuid}`，可升可降，target 包括其他 superuser。`PATCH /api/users/me` 唔收 `is_superuser`，帶咗回 `403`、`code: "FORBIDDEN"`（唔靜靜忽略，否則用戶會以為降咗權）。
- `PATCH /api/users/{uuid}` 收齊 `is_superuser` / `is_staff` / `is_active` 三個權限欄位；`is_active` 可 `false` 可 `true`，分別對應停用同還原。`is_active: true` 打落已 active 用戶 = no-op。
- **自我保護**：
  - 自我降權：`PATCH /api/users/me` 帶 `is_superuser`，或 `PATCH /api/users/{uuid}` 打自己帶 `is_superuser: false` → `403`、`code: "FORBIDDEN"`。
  - 自我停用：`DELETE /api/users/{uuid}` 或 `PATCH /api/users/{uuid}` 打自己帶 `is_active: false` → `409`、`code: "CONFLICT"`。
  - `is_staff` 冇自我限制（暫時 gate 唔到任何權限）。
- **Last-superuser 守門** —— 任何會令 active 嘅 `is_superuser = true` 用戶數目變成 0 嘅操作，一律回 `409`、`code: "LAST_SUPERUSER"`。適用範圍（全部指 target 係最後一個 active superuser）：
  - `DELETE /api/users/{uuid}`
  - `PATCH /api/users/{uuid}` 帶 `is_superuser: false`
  - `PATCH /api/users/{uuid}` 帶 `is_active: false`

  `PUT /api/users/{uuid}/password` **唔需要**呢個守門：改密碼唔會令任何人失去 superuser 身份，加落去只會令單一 superuser 改唔到自己密碼（要繞去 `/me/password`）。
- **第一個 superuser 冇 API 入口** —— `POST /api/auth/register` 唔收 `is_superuser` 亦唔發 token，`POST /api/users` 本身要 superuser。Cold start 用 CLI 指令（抄 Django `createsuperuser`）直接寫 DB，唔經 API。
- `username` / `email` 保留原樣大小寫，查詢同 unique 比對照原文（`Boss` 同 `boss` 係兩個唔同帳戶）。
- `username` / `email` 撞車（包括停用帳戶）回 `409 { "error": { "code": "CONFLICT" } }`。

## 停用 / 刪除嘅安全模型

`DELETE` 同 `PATCH /api/users/{uuid}` 帶 `is_active: false` 係軟刪（`is_active=false`），**唔會真係刪 row**。停用唔再係單向：superuser 可以用 `PATCH /api/users/{uuid}` 帶 `is_active: true` 還原（唯一還原入口，`DELETE` 冇對應 restore）。規則：

1. **Unique index 係 full index** —— `username` / `email` 嘅 unique constraint 覆蓋所有 row（包括停用），停用期間 username / email 一樣佔用，唔可以俾新帳戶註冊。所以還原嗰陣唔會撞名，亦避免任何帳號身份混淆風險。
2. **`is_active` 係唯一開關** —— 停用淨係 DB 一個 `UPDATE users SET is_active = false`，唔涉及 Redis。生效靠兩處檢查（見 `auth.md`「用戶狀態檢查」）：middleware 每個 request 都 load user 查 `is_active`，令手上嘅 access token 即刻失效；refresh endpoint 唔經 middleware，所以自己再查一次 `is_active`，令停用用戶換唔到新 token。停用有兩條入口（`DELETE /api/users/{uuid}`、`PATCH /api/users/{uuid}` 帶 `is_active: false`），兩者走同一段 code。還原同樣係一個 `UPDATE`，`is_active = true` 之後即刻可以照常 login / refresh。
3. **Login / refresh 唔再話俾停用用戶入** —— 停用後 login 回 `401`（同帳密錯一樣，唔透露係停用）；還原後即刻可以照常 login。
4. **還原唔改 `is_superuser`** —— 停用一個 superuser 再還原，佢照樣係 superuser（停用期間唔計入 last-superuser 守門嘅 active 數目）。

## Pagination

List endpoint 一律用同一個 envelope：

```json
{ "items": [], "total": 128, "limit": 20, "offset": 0 }
```

`limit` default 20、上限 100。
