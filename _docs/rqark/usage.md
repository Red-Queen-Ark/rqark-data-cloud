# Table

## usage_logs

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK（複合 `(id, created_at)` 之一），由服務以 uuid v7 產生 |
| `created_at` | datetime (UTC) | PK（複合 `(id, created_at)` 之一），即上游回應完成嘅時刻 |
| `user_id` | UUID | 發起呼叫嘅使用者，index |
| `api_key_id` | UUID | nullable，只有以 rqark API key 呼叫時才有值；access token 呼叫為 `null` |
| `session_id` | UUID | nullable，呼叫方自報 |
| `agent_id` | UUID | nullable，呼叫方自報 |
| `provider_id` | UUID | 呼叫所經嘅 provider |
| `source` | string(32) | 呼叫方自報嘅介面，例如 `TUI`、`AGENT_CLOUD`、`TASK` |
| `model` | string(120) | 上游回報嘅 model 名 |
| `prompt_tokens` | int32 | |
| `cached_tokens` | int32 | 預設 0，`prompt_tokens` 嘅子集 |
| `completion_tokens` | int32 | 預設 0 |
| `reasoning_tokens` | int32 | 預設 0，`completion_tokens` 嘅子集 |
| `total_tokens` | int32 | 上游自報嘅總數；上游冇報時取 `prompt_tokens + completion_tokens` |
| `is_compaction` | boolean | 預設 `false`，標記為壓縮對話嘅呼叫 |

索引：`idx_usage_user_time`、`idx_usage_user_model`、`idx_usage_user_key`，三者都以 `user_id` 為首欄；另有 `idx_usage_time`。

主鍵為 `(id, created_at)` 而唔係單獨 `id`，係為咗日後可以按 `created_at` 做範圍分區而唔需要改主鍵 —— Postgres 要求分區鍵必須出現在每一條 unique 約束內。

此表**冇 foreign key**。它係事實紀錄，唔係 session 嘅子表：session 可以早在用量失去意義之前就被刪除，而該列必須比 session 留得更久。`session_id` / `agent_id` / `provider_id` 只係參照，任何一個都可能指向已不存在的列。

計數欄用 int32：單次呼叫不可能超過 2^31 個 token，而此表永久保留；查詢時 `SUM()` 會自動擴寬為 bigint，因此窄欄位不會損失精度。

`total_tokens` 永不以 `prompt + cached + completion + reasoning` 計算：cached 係 prompt 嘅子集、reasoning 係 completion 嘅子集，相加會重複計算。

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| POST | `/api/usage` | `UsageQuery`，全部欄位可選；空 body 等同無篩選 | 列出可讀嘅用量列，`created_at` 新→舊，回 array |

`UsageQuery`：

```json
{
  "user_id": "4c2a3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
  "from": "2026-11-01T00:00:00Z",
  "to": "2026-12-01T00:00:00Z",
  "source": "TUI",
  "agent_id": "8e2f5d3b-2b2b-4b2b-9b2b-2b2b2b2b2b2b",
  "provider_id": "1b2c3d4e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
  "is_compaction": false,
  "api_key_name": "laptop"
}
```

`from` / `to` 為半開區間：`from` 包含、`to` 不包含；兩者同時出現且 `from` 不早於 `to` 時回 `400`。

回應每一列即 row 加上解析出嘅 `api_key_name`：

```json
{
  "id": "0192f3a4-1a1a-7a1a-8a1a-1a1a1a1a1a1a",
  "created_at": "2026-11-01T09:30:00Z",
  "user_id": "4c2a3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
  "api_key_name": "laptop",
  "source": "TUI",
  "model": "deepseek-flash",
  "session_id": "0f1e2d3c-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
  "agent_id": "8e2f5d3b-2b2b-4b2b-9b2b-2b2b2b2b2b2b",
  "provider_id": "1b2c3d4e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
  "is_compaction": false,
  "prompt_tokens": 1200,
  "cached_tokens": 800,
  "completion_tokens": 300,
  "reasoning_tokens": 0,
  "total_tokens": 1500
}
```

需要 `Authorization: Bearer <access_token>`，亦收 `rqark-` API key。清單**唔分頁、唔聚合**，直接回 array：客戶端自行繪圖，分頁會令它只能為範圍的一部分畫圖。

## 寫入路徑

用量由 chat proxy 在轉發回應時順帶記錄，唔係由呼叫方上報。

- 只有上游回 `2xx` 才會解析並記錄；`4xx` / `5xx` 整列不寫。
- 非串流回應會緩衝整個 body 解析 `usage`，上限 4 MiB；超過上限仍照樣轉發，但唔記錄。
- 串流時 proxy 無條件注入 `stream_options.include_usage = true`，並保留最後一個帶 `usage` 嘅 chunk。呼叫方唔可以關閉。
- 上游若拒收該參數，會回自己的錯誤，proxy 原樣轉發；該次呼叫唔記錄，但串流唔會中斷。
- `cached_tokens` 只讀 `usage.prompt_tokens_details.cached_tokens`。上游若把 cache 命中放在其他欄位（例如 DeepSeek 原生嘅 `prompt_cache_hit_tokens`），此欄為 `0`。
- 寫入經背景佇列（深度 1024、兩個 worker）。佇列滿或寫入超時（5 秒）時該列被丟棄，`UsageDropped()` 可觀察到丟棄次數 —— 聊天永不因為記帳而阻塞。
- `is_compaction`、`source`、`session_id`、`agent_id` 由呼叫方在 chat request body 自報，proxy 讀取後會從 body 移除，唔會轉發上游。`api_key_id` 亦會被移除但永不讀取：key 歸屬只來自身份驗證中間件，body 不能冒認其他 key。

## 規則

- **`user_id` 只准 superuser 帶入** —— 普通呼叫方永遠只讀到自己嘅列；帶了 `user_id` 會回 `400` 而唔係靜默忽略，免得呼叫方誤以為已被採納。
- **superuser 用同一條 route 讀全部人** —— 唔另設 admin path；唔帶 `user_id` 即係全體，帶了即係該使用者。
- **`api_key_name` 靠 join 解析** —— 以 left join 取 `api_keys.name`，所以 access token 呼叫嘅列、以及 key 已被刪除嘅列一樣會回傳，只係 `api_key_name` 為 `null`。
- **用量列唔會隨 session 消失** —— 刪除 session / agent / provider 唔影響已寫入嘅列。
