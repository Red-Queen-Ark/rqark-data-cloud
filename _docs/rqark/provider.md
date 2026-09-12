# Table

## providers
| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
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

GET / POST / PUT 回應即 `Provider` 本身，欄位同 `a.py` 一致（含 `api_key` 明文），加上 `id`、`created_at`、`updated_at`。

全部需要 `Authorization: Bearer <access_token>`。
