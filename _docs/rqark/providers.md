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
| `priority` | integer | 擁有者自己嘅排序，唔會傳去其他用戶 |
| `reasoning_effort` | string(16) | enum：`low` / `high` / `max`，nullable |
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
| POST | `/api/providers` | `{ "model_name": str, "api_key": str, "api_base": str, "priority": int, "name"?: str, "reasoning_effort"?: "low"/"high"/"max", "thinking"?: "enabled"/"disabled", "temperature"?: float, "top_p"?: float, "seed"?: int, "frequency_penalty"?: float, "presence_penalty"?: float, "combine"?: bool, "is_public"?: bool }` | 建立 provider |
| PUT | `/api/providers/{uuid}` | 同 POST，未帶入的欄位不變更 | 更新 provider（只有擁有者） |
| DELETE | `/api/providers/{uuid}` | — | 刪除 provider（只有擁有者） |
| PUT | `/api/providers/{uuid}/priority` | `{ "priority": int }` | 設定自己對一條人哋公用嘅 provider 嘅 priority override（未設過就係 `-99`） |

全部需要 `Authorization: Bearer <access_token>`。

## 代理呼叫模型

| 方法 | 路徑 | Body | 說明 |
|---|---|---|---|
| POST | `/api/chat` | OpenAI `POST /v1/chat/completions` 嘅 body，加一個 `provider_id` | 由 server 代打上游，`api_key` 唔會回客戶端 |
| POST | `/api/chat/{uuid}/chat/completions` | 同 OpenAI `POST /v1/chat/completions` 一樣 | 同上，provider 由 path 指定 |

- 兩條 route 都係同一個 proxy；path 版係為 OpenAI SDK 而設：`base_url` 設做 `{cloud_url}/api/chat/{uuid}`、`api_key` 設做 data cloud token，SDK 會自動 append `/chat/completions`。
- `provider_id` 必填（path 版由 path 帶），其餘欄位原樣送去 `api_base` 去尾斜線 + `/chat/completions`。
- `model` 唔填或者係空字串，用 provider 自己嘅 `model_name`。
- `stream: true` 時上游 SSE 逐段 relay，唔會 buffer 到最後。
- 可見性同 `GET /api/providers/{uuid}` 一致：自己嘅或者人哋公用嘅，其他一律 `404`。
- 上游嘅 status / headers / body 原樣回，`401`、`429` 唔會譯成 data cloud 自己嘅 error code。上游連唔到或者 `api_base` 唔係合法 URL 回 `502 BAD_GATEWAY`。
- run-scoped token 只可以打 path 版（`POST /api/chat/{uuid}` 唔准）；usage 記落 token 自己嘅 session，`source` 預設 `TASK`，body 帶嘅 `session_id` 會被覆蓋。

## 規則

- **每個 provider 屬於 token 對應嘅用戶**（`user_id`）—— `api_key` 係憑證。`PUT` / `DELETE /api/providers/{uuid}` 一律帶 `user_id = <token 用戶>`，只有擁有者改得；用其他用戶嘅 `uuid` 回 `404`（唔回 `403`，避免枚舉出 id）。
- **公用 provider**（`is_public = true`）—— admin（superuser）可以將自己嘅 provider 設做公用，之後其他用戶嘅 `GET /api/providers`、`GET /api/providers/models` 同 `GET /api/providers/{uuid}` 都會攞到佢（唔含 `api_key`）。非 superuser 想設 `is_public = true` 回 `403`。
- **`api_key` 只寫唔讀** —— `POST` / `PUT /api/providers` 照收 `api_key`，但 `GET`（list 同單條）一律唔會回。客戶端只傳 `provider_id` 去打 `/api/chat`，key 由 server 加。`PUT` 唔帶 `api_key` 即係保留原本嗰個。
- **priority 分三級，永遠唔會借擁有者嗰個** —— 對你生效嘅 `priority`（回應並排 `priority_override`）依次係：你自己落嘅 override；你自己嘅 provider 就係 `providers.priority`；人哋 share 俾你、你又未排過嘅，直接係 `-99`（`UnsetProviderPriority`）。所以擁有者改自己條 `priority` 唔會搞到你個 list，`-99` 排最前係因為排序由細到大。
- **override 只可以落喺人哋嘅 provider** —— 自己嘅 provider 用 `PUT /api/providers/{uuid}` 改 `priority`，喺 `/{uuid}/priority` 落 override 會回 `400`。睇唔到嘅 provider（唔係自己、又唔 public）一律 `404`。
- **override 只有寫入，冇清除** —— 落咗 override 之後，呢條 provider 對你嘅次序就係你自己嗰個數；冇 endpoint 還原返 `-99`，`PUT /api/providers/{uuid}/priority` 只收 `priority: int`，唔收 `null`。
- **un-share 唔會刪 override** —— 擁有者收返 `is_public` 之後，條 provider 唔再出現喺任何人嘅 list（filter 係睇 `providers.is_public`，唔係睇 `provider_priorities`），但 override 嗰行留住；將來再 share 返，之前設嘅 priority 會返嚟（唔會返去做 `-99`）。provider 硬刪時 override 由 `ON DELETE CASCADE` 一齊清。
- **排序** —— 兩個 list 都按「對你生效」嘅 priority 由細到大排，同分再按 `id`，所以係全序而且穩定；`GET /api/providers/models` 同 `GET /api/providers` 次序一致。未排過嘅公用 provider 全部係 `-99`，即係齊齊排最前，內部再按 `id` 定序。
- 回應嘅 DTO 有 `is_public`（係唔係公用）、`owned`（係唔係呼叫者自己嘅，即係改唔改得 provider 本身）、`priority_override`（係唔係呼叫者自己設嘅 priority），仍然唔含 `user_id`。
- `name` 冇 unique —— `Provider.__post_init__` 會將空 `name` 補成 `model_name`，同一個 model 可以有多條（唔同 `api_key` / `api_base`），排序靠 priority。
- 無設定嘅 nullable 欄位照 `null` 存，唔好當 `0` / `false` 解讀。
- 回應一律唔含 `user_id`，同 `agents.md` / `sessions.md` 一致。
