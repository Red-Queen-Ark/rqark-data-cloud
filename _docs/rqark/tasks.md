# tasks

定時任務：一個 task 係「一句 prompt + 一個 agent + 一個 provider + 一個排程」，由 agent-cloud 背景執行，用戶唔需要開住個 app。

權威喺 data-cloud：task 定義、run 記錄、`next_run_at` 計算同 claim 全部由佢話事。agent-cloud 只係 tick + 執行 + finalize。

# Table

## tasks

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `user_id` | UUID | FK → `users.id`，index，擁有者 |
| `name` | string(255) | unique with `user_id`（`idx_tasks_user_id_name`） |
| `prompt` | text | 每次 run 交俾 agent 嘅文字 |
| `agent_name` | string(255) | **軟引用**：寫入時驗證，claim 時再解析，唔存 id |
| `provider_name` | string(255) | 同上；provider 冇 `name` 時用 `model_name` |
| `timezone` | string(64) | IANA 名，預設 `UTC`，cron 用呢個時區計 |
| `enabled` | bool | 預設 `true` |
| `schedule_kind` | string(16) | `at` / `every` / `cron` |
| `run_at` | timestamptz | `at` 用 |
| `every_seconds` | integer | `every` 用，>= 60 |
| `cron_expr` | string(255) | `cron` 用，五欄 |
| `next_run_at` | timestamptz | nullable；**dispatch 唯一讀嘅排程欄位**，disabled 時必定 NULL |
| `run_now_requested_at` | timestamptz | nullable；手動 run 請求，claim 後清空 |
| `created_at` / `updated_at` | timestamptz | |

### 三種 schedule 互斥（`chk_tasks_schedule_kind`）

```sql
(schedule_kind = 'at'    AND run_at IS NOT NULL AND every_seconds IS NULL AND cron_expr IS NULL)
OR (schedule_kind = 'every' AND run_at IS NULL AND every_seconds >= 60 AND cron_expr IS NULL)
OR (schedule_kind = 'cron'  AND run_at IS NULL AND every_seconds IS NULL AND cron_expr IS NOT NULL)
```

### Index

| 名 | 欄位 | 用途 |
|---|---|---|
| `idx_tasks_user_id_name` | `(user_id, name)` UNIQUE | 同一用戶唔可以有兩個同名 task |
| `idx_tasks_enabled_next_run_at` | `(enabled, next_run_at)` | 掃 due task |

## task_runs

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `task_id` | UUID | FK → `tasks.id`，`ON DELETE CASCADE` |
| `user_id` | UUID | 重複 task 嘅擁有者，令 claim 唔使 join `tasks` 就查到該用戶嘅 run |
| `status` | string(16) | `running` / `ok` / `error` / `cancelled` / `skipped` / `lost` |
| `trigger` | string(16) | `schedule` / `manual` |
| `scheduled_at` | timestamptz | 呢條 run 屬於邊個 slot；`manual` 用請求時間 |
| `started_at` | timestamptz | 由 claim 寫入 —— 有 `running` 就代表真係開始咗 |
| `finished_at` | timestamptz | |
| `error` | text | 預設 `''` |
| `session_id` | UUID | run 寫 transcript 落邊個 session；跳過嘅 run 冇 |

### Index

| 名 | 欄位 | 用途 |
|---|---|---|
| `idx_task_runs_task_id_scheduled_at` | `(task_id, scheduled_at)` UNIQUE | **去重**：同一個 slot 最多一條 run |
| `idx_task_runs_task_id_status` | `(task_id, status)` | 查 task 有冇 running |
| `idx_task_runs_status_started_at` | `(status, started_at)` | 掃 stale run |

冇 per-user index：同一用戶可以同時跑多個 task（見 I3）。

`error` 嘅值：

| 值 | 意義 |
|---|---|
| `lost` | claim 成功但進程死亡，掃 stale 時標記。**唔重試** |
| `missed` | `at` task 過咗 `TASK_AT_GRACE` 先 claim 到，唔 dispatch |
| `AGENT_NOT_FOUND` | claim 時 `agent_name` 解析唔到 |
| `PROVIDER_NOT_FOUND` | 同上，provider |
| `timeout` | runner 用（A2） |

# `next_run_at` 規則

| 情況 | 結果 |
|---|---|
| 寫入時 `enabled = true` | 計返一個：`at` → `run_at`；`every` → `now + every_seconds`；`cron` → `Next(now)` |
| 寫入時 `enabled = false` | `NULL` |
| `PUT` 改 schedule / timezone | 重算 |
| `PUT` `enabled` false → true | 重算 |
| `PUT` `enabled` true → false | `NULL` |
| claim 一條 `at` run | `enabled = false`，`next_run_at = NULL` |
| claim 一條 `every` run | 該 gap 嘅 grid 上第一個 > `now` 嘅時刻（**唔補跑**） |
| claim 一條 `cron` run | `Next(now)` |
| `run_now` | **唔改** |
| cron 五年內都唔會 fire（例如 `0 0 30 2 *`） | 寫入時 400；claim 時 disable |

`every` 嘅 grid 由 Unix epoch 起算，所以 gap 整除一小時／一日就會落在整點：

- `every_seconds = 60` → 每分鐘的 `:00`
- `every_seconds = 3600` → 每小時的 `HH:00`
- `every_seconds = 86400` → 每日 `00:00` UTC
- `every_seconds = 90` → `:00`、`:01:30`、`:03:00`…

`next_run_at` 一定係 grid 上**第一個仍在前方**嘅時刻，所以 miss 咗就係 miss，唔會累積 backlog，亦唔會因為 tick 遲一兩秒而越跑越偏。

# API

## 用戶向

| Method | Path | Body | 說明 |
|---|---|---|---|
| GET | `/api/tasks` | — | 全部 task，按 `name` 排序，**唔分頁** |
| POST | `/api/tasks` | `{ name, prompt, agent_name, provider_name, timezone?, enabled?, schedule_kind, run_at?, every_seconds?, cron_expr? }` | 建 task，回 `201` + `Location` |
| GET | `/api/tasks/{uuid}` | — | 單一 task |
| PUT | `/api/tasks/{uuid}` | 同上（全部 optional，`schedule_kind` 一帶就換成個 schedule） | 更新 |
| DELETE | `/api/tasks/{uuid}` | — | 連 run 一齊刪，回 `204` |
| GET | `/api/tasks/{uuid}/runs?limit=&offset=` | — | `utils.Page`，`scheduled_at` 新→舊 |
| POST | `/api/tasks/{uuid}/run` | — | 手動 run，回 `202` |

全部收 `Authorization: Bearer <access_token>` 或 `rqark-` API key。跨用戶一律 `404`。

驗證：

- `schedule_kind` 三選一，只准帶對應嗰個欄位
- `every_seconds >= 60`
- 兩個名各自要解析到**恰好一個**對方可用嘅 agent / provider，多過一個回 `409` + `AGENT_AMBIGUOUS` / `PROVIDER_AMBIGUOUS`
- `at` 嘅 `run_at` 早過 `now - TASK_AT_GRACE` → `400`
- `cron_expr` 只收五欄，`@daily` / `@every 1s` / `TZ=...` 前綴一律 `400`
- `(user_id, name)` 重複 → `409`

`run_now`：`enabled = false` → `409`；已有 `running` → `409`。

## 內部

`X-Internal-Token` header（**唔收** `Bearer`）。`INTERNAL_TOKEN` 未設 = 全部 `401`。

| Method | Path | Body | 說明 |
|---|---|---|---|
| POST | `/api/internal/tasks/claim` | `{ "limit": int }` | 回 `{ runs: [...] }` |
| PATCH | `/api/internal/tasks/{task_uuid}/runs/{run_uuid}` | `{ status, error?, session_id?, finished_at? }` | 只准 `running` → 終態，重複／遲到回 `409` |

呢兩條唔會出現喺公開 swagger（`swag init --exclude internal/api/internal`）。

claim 一次做齊：掃 stale → 揀 due（`FOR UPDATE SKIP LOCKED`）→ 建 session（`source='TASK'`）→ insert run → 推進 `next_run_at` → 簽 run token。全程一個 transaction。

揀 due 嘅條件：`enabled = true` **且**（`next_run_at <= now` **或** 有 `run_now_requested_at`），並且該 task 冇 `running` run，同埋該用戶 `is_active = true`。暫停咗嘅 task 一律唔會被揀，即使身上有 pending 嘅 `run_now`。

停用帳號唔會停佢啲 task（重新啟用時會照跑返），只係 claim 時跳過。所以停用期間 `next_run_at` 會停留喺過去，重新啟用後會補跑一格，之後即刻跳返上 grid。

同一用戶嘅多個 task 會喺同一次 claim 一齊出，全部共用該用戶嘅 sandbox（`rqark-user-{slug}-workspace`）。呢個係刻意嘅：沙盒係用戶嘅，唔係 run 嘅。代價係兩個 run 同時寫 `/workspace` 會互相踩，所以 runner 唔會喺 run 完之後 stop container。

claim 回嘅 token 係 run-scoped：`scope='run'`，只可以打自己 `session_id` 嘅 `GET` / `PUT /api/sessions/{uuid}` 同 `GET /api/sessions/{uuid}/messages`，其餘一律 `403`。

# 不變式

| # | 不變式 | 後果 |
|---|---|---|
| I1 | `next_run_at` **只**喺 claim-insert 成功嘅同一 transaction 內推進 | backlog 永不累積 → fire-once |
| I2 | claim 成功 = 即時 dispatch，`started_at` 由 claim 寫入 | 「running」真係 running |
| I3 | 一個 task 同時最多一條 `running`；**同一用戶可以有多個 task 同時跑** | 短週期撞長 run 只阻住該 task、唔累積；用戶想兩個 task 一齊跑就跑 |
| I4 | finalize 只准 `running` → 終態 | 遲到／重複 PATCH 被拒 |

# Config

| 變數 | 預設 | 說明 |
|---|---|---|
| `INTERNAL_TOKEN` | 空 | 內部 API 嘅 shared secret；空 = 拒絕所有內部請求 |
| `TASK_AT_GRACE` | 300 | `at` task 遲幾多秒都仲跑 |
| `TASK_RUN_TIMEOUT` | 600 | 一條 run 最多跑幾久 |
| `TASK_RUN_TOKEN_BUFFER` | 120 | run token TTL = `TASK_RUN_TIMEOUT` + 呢個 |
| `TASK_RUN_STALE_AFTER` | `TIMEOUT + BUFFER` | 過咗就掃成 `lost`；**必須 > `TASK_RUN_TIMEOUT`** |
