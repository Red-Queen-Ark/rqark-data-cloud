# Agent Defaults

記錄每個用戶喺唔同 session source（平台）默認用邊個 agent，即係「雲端版本地默認 agent 設定」，俾 client 開新 session 前先查一次要用邊個 agent。

`source` 用嘅值同 `sessions.source` 一樣（例如 `TUI`、`AGENT_CLOUD`），所以 client 可以直接用自己 upload session 時帶嘅 source 去查默認 agent。

# Table

## agent_defaults

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `user_id` | UUID | FK → `users.id`，index，擁有者 |
| `source` | string(32) | session 來源，identity |
| `agent_id` | UUID | FK → `agents.id`，index，`ON DELETE CASCADE` |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

Unique index `(user_id, source)` —— 每個用戶每個 `source` 最多一個默認 agent。

`agent_id` 用 `ON DELETE CASCADE`：一個 agent 被刪，指向佢嘅默認行一齊清，唔會留住指向空氣嘅 id。

`agent_id` 亦會因為 un-share 而清：一個公用 agent 被擁有者收返 `is_public` 時，**其他用戶**指向佢嘅默認行即時刪走（擁有者自己嘅默認行留住，因為 un-share 唔會將個 agent 從擁有者手上攞走）。詳見 `agents.md` 嘅「公用 agent」。

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/agent-defaults` | — | 回當前用戶全部默認 agent，array（唔分頁），`source` 升序 |
| GET | `/api/agent-defaults/{source}` | — | 取得一個 `source` 嘅默認 agent；冇設過回 `404` |
| PUT | `/api/agent-defaults/{source}` | `{ "agent_id": uuid }` | 新增或覆寫一個 `source` 嘅默認 agent（upsert），回 `200` |
| DELETE | `/api/agent-defaults/{source}` | — | 刪一個 `source` 嘅默認，回 `204` |

全部需要 `Authorization: Bearer <access_token>`，亦收 `rqark-` API key（同 `/api/agents`、`/api/providers`、`/api/sessions`、`/api/user-config` 一致）。

GET 回應：

```json
[
  {
    "id": "3f9a1c7e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
    "source": "AGENT_CLOUD",
    "agent_id": "8c2b4d0f-2b2b-4b2b-9b2b-2b2b2b2b2b2b",
    "created_at": "2026-10-14T11:16:11Z",
    "updated_at": "2026-10-14T11:16:11Z"
  },
  {
    "id": "5e1f2a3b-3c3c-4c3c-8c3c-3c3c3c3c3c3c",
    "source": "TUI",
    "agent_id": "8c2b4d0f-2b2b-4b2b-9b2b-2b2b2b2b2b2b",
    "created_at": "2026-10-14T11:16:11Z",
    "updated_at": "2026-10-14T11:16:11Z"
  }
]
```

PUT 回單一 object（同 array 內嘅元素一樣），唔係 array。`GET /api/agent-defaults/{source}` 回同一個 object。

## 規則

- **`source` 係 identity** —— unique `(user_id, source)`。PUT 唔理個 `source` 存在未，一律 upsert：存在就改 `agent_id`，唔存在就新增。
- **`source` 格式** —— 長度 1–32，只准 `[A-Za-z0-9_.-]`（URL-safe，可直接放 path）。唔合法回 `400`。上限 32 同 `sessions.source` 一致；因為要放入 path，所以比 `sessions.source` 多一層字元限制。
- **`agent_id` 必填** —— 一定要係呼叫者可以用嘅 agent：自己嘅，或者人哋設做公用嘅。指去一個呼叫者用唔到嘅 agent（唔存在、或者係人哋嘅但唔公用）一律回 `404`，唔回 `403`，避免枚舉 id（同其他 resource 一致）。公用 agent 被 un-share 之後，指向佢嘅默認行會即刻被刪（見下）。
- **`created_at` / `updated_at` 由 server 管** —— request 帶咗都忽略。
- **唔分頁** —— 回 array（同 `/api/agents`、`/api/providers`、`/api/sessions`、`/api/user-config` 一致）：呢啲係用戶自己嘅默認設定，一次過攞齊先夠做本地覆寫。
- **跨用戶** —— 所有 query 都帶 `user_id = <token 用戶>`；`GET` / `DELETE` 一個冇設過默認嘅 `source` 回 `404`。
- 回應唔含 `user_id`，同其他 resource doc 一致。

## 錯誤格式

```json
{ "error": { "code": "BAD_REQUEST", "message": "..." } }
```

| 場景 | HTTP | code |
|---|---|---|
| 缺 token / token 或 API key 無效 | 401 | `UNAUTHORIZED` |
| `source` 唔合法 / `agent_id` 唔係 UUID / body 唔係合法 JSON | 400 | `BAD_REQUEST` |
| `agent_id` 唔屬於呼叫者 | 404 | `NOT_FOUND` |
| `GET` / `DELETE` 一個冇設過默認嘅 `source` | 404 | `NOT_FOUND` |
| 其他內部錯誤 | 500 | `INTERNAL` |
