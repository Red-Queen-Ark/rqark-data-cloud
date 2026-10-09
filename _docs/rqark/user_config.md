# User Config

統一管理用戶設定（`theme`、`language` 等），俾 client 當「雲端版本地 config 檔」讀寫，唔使淨靠 Web 自己 localStorage。

# Table

## user_configs

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `user_id` | UUID | FK → `users.id`，index，擁有者 |
| `key` | string(128) | 設定名，identity |
| `value` | jsonb | 設定值 |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

Unique index `(user_id, key)` —— 每個用戶同一個 `key` 得一筆。

`value` 用 jsonb：保留 type（`"dark"` / `true` / `24` / `{ ... }`），client 毋須自行約定格式。寫入原樣、讀回語意一樣（jsonb 會正規化 key 次序同空白）。

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/user-config` | — | 回當前用戶全部 config，array（唔分頁），`key` 升序 |
| PUT | `/api/user-config/{key}` | `{ "value": any }` | 新增或覆寫一個 key（upsert），回 `200` |
| DELETE | `/api/user-config/{key}` | — | 刪一個 key，回 `204` |

全部需要 `Authorization: Bearer <access_token>`，亦收 `rqark-` API key（同 `/api/agents`、`/api/providers`、`/api/sessions` 一致）。

GET 回應：

```json
[
  {
    "id": "3f9a1c7e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
    "key": "theme",
    "value": "dark",
    "created_at": "2026-10-09T15:16:11Z",
    "updated_at": "2026-10-09T15:16:11Z"
  },
  {
    "id": "8c2b4d0f-2b2b-4b2b-9b2b-2b2b2b2b2b2b",
    "key": "language",
    "value": "zh-HK",
    "created_at": "2026-10-09T15:16:11Z",
    "updated_at": "2026-10-09T15:16:11Z"
  }
]
```

PUT 回單一 object（同 array 內嘅元素一樣），唔係 array。

冇 `GET /api/user-config/{key}`：`GET /api/user-config` 已經回晒全部，單一 key 冇細節頁（同 `api-keys.md`）。

## 規則

- **`key` 係 identity** —— unique `(user_id, key)`。PUT 唔理個 key 存在未，一律 upsert：存在就改 `value`，唔存在就新增。
- **`key` 格式** —— 長度 1–128，只准 `[A-Za-z0-9_.-]`（URL-safe，可直接放 path）。唔合法回 `400`。
- **`value` 係任意 JSON 值** —— object / array / string / number / boolean 都得，原 type 保存。`null` 唔收（清一個 key 用 `DELETE`），回 `400`。
- **`value` 上限 64KB** —— 以編碼後 JSON 長度計；寫入前清 NUL（jsonb 收唔到 U+0000）。
- **`created_at` / `updated_at` 由 server 管** —— request 帶咗都忽略。
- **唔分頁** —— 回 array（同 `/api/agents`、`/api/providers`、`/api/sessions` 一致）：呢個係用戶自己嘅 config，一次過攞齊先夠做本地覆寫。
- **跨用戶** —— 所有 query 都帶 `user_id = <token 用戶>`；DELETE 一個唔存在嘅 key 回 `404`。
- 回應唔含 `user_id`，同其他 resource doc 一致。

## 錯誤格式

```json
{ "error": { "code": "BAD_REQUEST", "message": "..." } }
```

| 場景 | HTTP | code |
|---|---|---|
| 缺 token / token 或 API key 無效 | 401 | `UNAUTHORIZED` |
| `key` 唔合法 / `value` 過大 / body 唔係合法 JSON | 400 | `BAD_REQUEST` |
| DELETE 一個唔存在嘅 key | 404 | `NOT_FOUND` |
| 其他內部錯誤 | 500 | `INTERNAL` |
