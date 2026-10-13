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
| `priority` | integer | 擁有者設定嘅預設排序 |
| `reasoning_effort` | string(16) | enum：`high` / `max`，nullable |
| `thinking` | string(16) | enum：`enabled` / `disabled`，nullable |
| `temperature` | float | nullable |
| `top_p` | float | nullable |
| `seed` | integer | nullable |
| `frequency_penalty` | float | nullable |
| `presence_penalty` | float | nullable |
| `combine` | boolean | 預設 `false` |
| `is_public` | boolean | 預設 `false`，`true` 表示公用 |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

## provider_priorities
| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `user_id` | UUID | FK → `users.id`，index，寫低 override 嘅用戶 |
| `provider_id` | UUID | FK → `providers.id`，index，`ON DELETE CASCADE` |
| `priority` | integer | 該用戶對呢條 provider 自己嘅排序 |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

Unique index `(user_id, provider_id)` —— 每個用戶對每條 provider 最多一個 override。

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/providers` | — | 列出你可使用嘅 provider：自己嘅加其他人公用嘅 |
| GET | `/api/providers/{uuid}` | — | 取得單一 provider（自己嘅或者人哋公用嘅） |
| POST | `/api/providers` | `{ "model_name": str, "api_key": str, "api_base": str, "priority": int, "name"?: str, "reasoning_effort"?: "high"/"max", "thinking"?: "enabled"/"disabled", "temperature"?: float, "top_p"?: float, "seed"?: int, "frequency_penalty"?: float, "presence_penalty"?: float, "combine"?: bool, "is_public"?: bool }` | 建立 provider |
| PUT | `/api/providers/{uuid}` | 同 POST，未帶入的欄位不變更 | 更新 provider（只有擁有者） |
| DELETE | `/api/providers/{uuid}` | — | 刪除 provider（只有擁有者） |
| PUT | `/api/providers/{uuid}/priority` | `{ "priority": int }` | 設定自己對一條人哋公用嘅 provider 嘅 priority override |
| DELETE | `/api/providers/{uuid}/priority` | — | 清走自己嘅 priority override，改回跟擁有者 |

全部需要 `Authorization: Bearer <access_token>`。

## 規則

- **每個 provider 屬於 token 對應嘅用戶**（`user_id`）—— `api_key` 係憑證。`PUT` / `DELETE /api/providers/{uuid}` 一律帶 `user_id = <token 用戶>`，只有擁有者改得；用其他用戶嘅 `uuid` 回 `404`（唔回 `403`，避免枚舉出 id）。
- **公用 provider**（`is_public = true`）—— admin（superuser）可以將自己嘅 provider 設做公用，之後其他用戶嘅 `GET /api/providers`、`GET /api/providers/models` 同 `GET /api/providers/{uuid}` 都會攞到佢（連 `api_key`，因為客戶端要靠佢連模型）。非 superuser 想設 `is_public = true` 回 `403`。
- **priority 分兩層** —— `providers.priority` 係擁有者設定嘅預設；每個用戶可以喺 `provider_priorities` 寫低自己嘅 override。回應嘅 `priority` 係「對你生效」嘅值（有 override 就用 override，冇就用擁有者嗰個），`priority_override` 係你自己嗰個 override（跟擁有者時係 `null`）。所以改 override 只影響自己，唔會改到其他人嘅次序，亦唔會改到擁有者嗰條 row。
- **override 只可以落喺人哋嘅 provider** —— 自己嘅 provider 用 `PUT /api/providers/{uuid}` 改 `priority`，喺 `/{uuid}/priority` 落 override 會回 `400`。睇唔到嘅 provider（唔係自己、又唔 public）一律 `404`。
- **清 override** —— `DELETE /api/providers/{uuid}/priority` 刪走自己嗰行，priority 即刻跟返擁有者；本來冇 override 回 `404`。
- **un-share 唔會刪 override** —— 擁有者收返 `is_public` 之後，條 provider 唔再出現喺任何人嘅 list（filter 係睇 `providers.is_public`，唔係睇 `provider_priorities`），但 override 嗰行留住；將來再 share 返，之前設嘅 priority 會返嚟。provider 硬刪時 override 由 `ON DELETE CASCADE` 一齊清。
- **排序** —— 兩個 list 都按「對你生效」嘅 priority 由細到大排，同分再按 `id`，所以係全序而且穩定；`GET /api/providers/models` 同 `GET /api/providers` 次序一致。
- 回應嘅 DTO 有 `is_public`（係唔係公用）、`owned`（係唔係呼叫者自己嘅，即係改唔改得 provider 本身）、`priority_override`（係唔係呼叫者自己設嘅 priority），仍然唔含 `user_id`。
- `name` 冇 unique —— `Provider.__post_init__` 會將空 `name` 補成 `model_name`，同一個 model 可以有多條（唔同 `api_key` / `api_base`），排序靠 priority。
- 無設定嘅 nullable 欄位照 `null` 存，唔好當 `0` / `false` 解讀。
- 回應一律唔含 `user_id`，同 `agents.md` / `sessions.md` 一致。
