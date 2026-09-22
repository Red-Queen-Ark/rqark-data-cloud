# Table

## providers
| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `user_id` | UUID | FK → `users.id`，index，擁有者 |
| `name` | string(255) | 預設 `''` |
| `model_name` | string(255) | |
| `api_key` | string(255) | |
| `api_base` | string(255) | |
| `priority` | integer | |
| `reasoning_effort` | string(16) | enum：`high` / `max`，nullable |
| `thinking` | string(16) | enum：`enabled` / `disabled`，nullable |
| `temperature` | float | nullable |
| `top_p` | float | nullable |
| `seed` | integer | nullable |
| `frequency_penalty` | float | nullable |
| `presence_penalty` | float | nullable |
| `combine` | boolean | 預設 `false` |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/providers` | — | 列出全部 provider |
| GET | `/api/providers/{uuid}` | — | 取得單一 provider |
| POST | `/api/providers` | `{ "model_name": str, "api_key": str, "api_base": str, "priority": int, "name"?: str, "reasoning_effort"?: "high"/"max", "thinking"?: "enabled"/"disabled", "temperature"?: float, "top_p"?: float, "seed"?: int, "frequency_penalty"?: float, "presence_penalty"?: float, "combine"?: bool }` | 建立 provider |
| PUT | `/api/providers/{uuid}` | 同 POST，未帶入的欄位不變更 | 更新 provider |
| DELETE | `/api/providers/{uuid}` | — | 刪除 provider |

全部需要 `Authorization: Bearer <access_token>`。

## 規則

- **每個 provider 屬於 token 對應嘅用戶**（`user_id`）—— `api_key` 係憑證，所有查詢都帶 `user_id = <token 用戶>`，唔會撈到其他用戶嘅 provider；用其他用戶嘅 `uuid` 回 `404`（唔回 `403`，避免枚舉出 id）。
- `name` 冇 unique —— `Provider.__post_init__` 會將空 `name` 補成 `model_name`，同一個 model 可以有多條（唔同 `api_key` / `api_base`），排序靠 `priority`。
- 無設定嘅 nullable 欄位照 `null` 存，唔好當 `0` / `false` 解讀。
- 回應一律唔含 `user_id`，同 `agents.md` / `sessions.md` 一致。
