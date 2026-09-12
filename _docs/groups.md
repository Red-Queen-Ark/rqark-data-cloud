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
| `group_id` | UUID | FK → `groups.id`, PK |

# API
| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/groups` | — | 列出全部 group |
| GET | `/api/groups/{uuid}` | — | 取得單一 group |
| POST | `/api/groups` | `{ "name": str }` | 建立 group（需 superuser） |
| PUT | `/api/groups/{uuid}` | `{ "name": str }` | 更新 group（需 superuser） |
| DELETE | `/api/groups/{uuid}` | — | 刪除 group（需 superuser） |
| POST | `/api/groups/{group_id}/users` | `{ "user_id": UUID }` | 加入成員（需 superuser） |
| DELETE | `/api/groups/{group_id}/users/{user_id}` | — | 移除成員（需 superuser） |

全部需要 `Authorization: Bearer <access_token>`。
