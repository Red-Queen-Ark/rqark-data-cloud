# Table
## groups

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `name` | string(150) | unique, index |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

## user_groups
| 欄位 | 型別 | 備註 |
|---|---|---|
| `user_id` | UUID | FK → `users.id`, PK |
| `group_id` | UUID | FK → `groups.id`, PK, 另外獨立 index |

PK 係 `(user_id, group_id)`，leading column 係 `user_id`，所以 `WHERE group_id = ?`（成員列表）用唔到 PK index，要另外 `INDEX (group_id)`。

# API
| Method | Path | Request body | 權限 | 說明 |
|---|---|---|---|---|
| GET | `/api/groups` | — | 登入 | 支援 `?limit=&offset=`。只回 group 本身，唔含成員 |
| POST | `/api/groups` | `{ "name" }` | superuser | 回 `201` + `Location` |
| GET | `/api/groups/{uuid}` | — | 登入 | |
| PATCH | `/api/groups/{uuid}` | `{ "name" }` | superuser | |
| DELETE | `/api/groups/{uuid}` | — | superuser | 連 `user_groups` 一齊刪，回 `204` |
| GET | `/api/groups/{uuid}/users` | — | superuser | 支援 `?limit=&offset=` |
| PUT | `/api/groups/{uuid}/users/{user_uuid}` | — | superuser | 加入成員，idempotent，回 `204` |
| DELETE | `/api/groups/{uuid}/users/{user_uuid}` | — | superuser | 回 `204` |

全部需要 `Authorization: Bearer <access_token>`。

## 規則

- `GET /api/groups/{uuid}/users` 回嘅係 user DTO（同 `GET /api/users/{uuid}` 一樣），加 pagination envelope。
- **`GET /api/groups/{uuid}/users` 要 superuser，唔係「登入」** —— 佢回 user DTO。如果放「登入」就等於任何普通用戶都可以 `GET /api/groups` 攞 id 再枚舉全公司用戶名／email，繞過 `GET /api/users` 嘅 superuser 限制。Group 本身（name）唔敏感，所以 `GET /api/groups` 同 `GET /api/groups/{uuid}` 維持「登入」。
- **唔支援 `?q=`**（同 `/api/users` 唔同），group 成員數量一般細，冇搜尋需求。如果日後要加，先喺度更新。
- `POST /api/groups` / `PATCH` 撞 `name` 回 `409 { "error": { "code": "CONFLICT" } }`。
- `DELETE /api/groups/{uuid}` 係 hard delete（group 冇 soft delete 需求），`user_groups` cascade 刪。
- 停用用戶（`is_active=false`）**唔會**自動從 group 移除，但 login / refresh 已經入唔到，等於凍結。

## Pagination

同 users：

```json
{ "items": [], "total": 128, "limit": 20, "offset": 0 }
```

`limit` default 20、上限 100。
