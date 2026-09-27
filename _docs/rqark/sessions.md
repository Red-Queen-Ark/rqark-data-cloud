# Table

## sessions

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK，等於 app 本地生成嘅 session uuid |
| `user_id` | UUID | FK → `users.id`，index，擁有者 |
| `title` | string(255) | 預設 `''`，由 app `gen_title` 用 LLM 生成（prompt 限 8 words） |
| `plan_items` | jsonb | 預設 `'[]'`，`[{ "id": str, "content": str, "status": str }]` |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

`plan_items[].status` ∈ `pending` / `in_progress` / `completed` / `cancelled`。

## session_messages

| 欄位 | 型別 | 備註 |
|---|---|---|
| `session_id` | UUID | FK → `sessions.id`，PK，`ON DELETE CASCADE` |
| `seq` | integer | PK，0-based，session 內由 0 起連續 |
| `role` | string(16) | `user` / `assistant` / `tool` / `system` / `summary` |
| `data` | jsonb | 整條 message，同 app `to_dict()` 一致 |
| `created_at` | datetime (UTC) | nullable，由 `data.created_at` 抽出；`summary` 冇 `created_at`，存 `null` |

PK 係 `(session_id, seq)`。得 `role` 同 `created_at` 抽做 column（方便統計 / 過濾），`data` 度照樣保留，其餘一律入 `data`。

`data` 按 `role` 嘅形狀（同 rqark-app `rqark_app/types/session.py` 一致）：

```jsonc
// user：content 恒為 parts array，純文字都一樣
{ "role": "user", "content": [ { "type": "text", "text": "..." }, { "type": "image_url", "image_url": { "url": "..." } } ], "created_at": "..." }
// assistant
{ "role": "assistant", "content": "...", "tool_calls": [ { "id": "...", "type": "function", "function": { "name": "...", "arguments": "..." } } ], "reasoning_content": "...", "created_at": "..." }
// tool
{ "role": "tool", "tool_call_id": "...", "content": "...", "created_at": "..." }
// system
{ "role": "system", "content": "...", "created_at": "..." }
// summary：壓縮出嚟嘅摘要，只係當 chunk 界線插入 history，唔會刪走之前嘅 message
{ "role": "summary", "content": "..." }
```

`tool_calls` / `reasoning_content` 冇就唔出。

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/sessions` | — | 列出自己嘅 session metadata（**唔含** messages），回 array，`updated_at` 新→舊 |
| POST | `/api/sessions` | `{ "uuid"?: str, "title"?: str, "plan_items"?: [...] }` | 建立 session，回 `201` + `Location` |
| GET | `/api/sessions/{uuid}` | — | 取得 session metadata（**唔含** messages） |
| PUT | `/api/sessions/{uuid}` | `{ "title"?: str, "plan_items"?: [...], "history"?: [message] }`，未帶入的欄位不變更 | 更新 session；帶 `history` 就成批覆寫 messages |
| DELETE | `/api/sessions/{uuid}` | — | 連 messages 一齊刪，回 `204` |
| GET | `/api/sessions/{uuid}/messages` | — | 回 array，`seq` 升序 |

全部需要 `Authorization: Bearer <access_token>`，亦收 `rqark-` API key（同 `/api/agents`、`/api/providers` 一致：app 就係用 key 同步 session 數據）。

`sessions` 系列回應即 row 減 `user_id`（`id`、`title`、`plan_items`、`created_at`、`updated_at`），唔含 `history`；`/messages` 每項係 `{ "seq": int, "role": str, "data": object, "created_at": str|null }`。

兩個 list 都**唔分頁**，直接回 array（同 `/api/agents`、`/api/providers` 一致）。理由同 agents 一樣：呢啲係 app 當「雲端版本地檔」同步用，`JSONLStorage.load()` 本身就係一次過讀齊所有 session 同整條 history，分頁對 app 冇用；管理向清單（`api-keys` / `users` / `groups`）先出 `Page` envelope。

## 規則

- **`role` 同 `created_at` 係唯二抽得出嚟嘅欄位** —— cloud 只讀 `data.role` 存去 column，讀 `data.created_at`（RFC 3339）抄一份；`role` 唔係五個之一、或者 `created_at` 唔係合法時間就回 `400`。`data` 其餘一律唔睇。
- **`id` 由 client 話事** —— app 自己生成 session uuid，用佢做本地檔名（`{session_id}.jsonl`）同 dir cache value，所以 POST 收 client 帶嘅 `uuid`（唔帶先由 server 生成）。同一個 user 用返已存在嘅 `uuid` POST 回 `409`、`code: "CONFLICT"`。
- **DirCache 唔上 cloud** —— app 用 `dir_hash → session_id` 記住「呢個目錄上次開邊個 session」，係裝置本地狀態，cloud 唔存 dir 資訊；要跨裝置續傾就由 client 自己揀 session。
- **`history` 成批覆寫** —— app 每次 save 都重寫成個 session（`JSONLStorage._write_file`），所以 PUT 帶 `history` 就係用嗰個 array 完全取代 messages：一個 transaction 內 delete + 重新 insert，`seq` 由 0 再編。冇帶 `history` 就唔郁 messages（例如 `gen_title` 淨係改 `title`）。
- **messages 冇獨立 id** —— app 嘅 message 冇 id，次序就係 identity，所以 PK 用 `(session_id, seq)`。呢點配合「成批覆寫」剛好：app 出錯重試或者續寫時會重寫最後一個 chunk（`Session.update_history_to_last_chunk`），成批 PUT 天然覆蓋到，唔使逐條 diff。
- **`data` 原樣存** —— cloud 唔拆 message 內部結構。rqark-core 日後加 message 欄位（例如 app 現時 `to_dict()` 冇寫嘅 `usage`）唔使改 cloud schema，直接 round-trip。
- **`created_at` / `updated_at` 由 server 管** —— request 帶咗都忽略。session 內部每條 message 自己嘅 `created_at` 照存（`data` 同 column 兩邊都有，column 版係為咗唔拆 JSON 就查得到時間）。
- **`sessions` 係 metadata-only 讀取** —— `GET /api/sessions` 同 `GET /api/sessions/{uuid}` 都唔回 `history`，要 message 就另外拉 `/messages`，避免 list 時拉爆 payload。
- **跨用戶** —— session 屬於 token 對應用戶，任何 query 都帶 `user_id = <token 用戶>`；用其他用戶嘅 `uuid` 一律回 `404`。
