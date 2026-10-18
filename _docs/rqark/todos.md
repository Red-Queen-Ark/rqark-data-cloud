# Todos

每個用戶自己嘅 todo list，一個 flat 清單，唔分頁，同 `/api/agents`、`/api/user-config` 一樣係「雲端版本地檔」。

# Table

## todo_items

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `user_id` | UUID | FK → `users.id`，擁有者 |
| `title` | string(255) | 寫入時 trim，空字串回 `400` |
| `description` | text | 預設 `''`，上限 8192 |
| `done` | bool | 預設 `false` |
| `done_at` | datetime (UTC) null | 由 server 依 `done` 推導，唔可以由 request 寫 |
| `due_at` | datetime (UTC) null | 只作顯示／排序，冇提醒 |
| `sort_order` | int | 預設 `0`，手動排序用 |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

Index：`(user_id, done)`、`(user_id, due_at)`、`(user_id, sort_order)`。三個都以 `user_id` 行頭，所以唔另開單欄 `user_id` index；FK 檢查用同一批。

冇 soft delete：`DELETE` 係 hard delete，同 `user_configs` 一致。

`done_at` 由 server 管：`done` 由 false 轉 true 時寫入當時時間（重複 tick 一個已 done 嘅 item 唔會改動原本時間）；`done` 轉 false 時清 NULL。所以唔會出現 done 但冇時間、或者未 done 但有時間嘅 row。

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/todos` | — | 回當前用戶全部 todo，array（唔分頁），未完成行先 |
| POST | `/api/todos` | `{ title, description?, due_at?, sort_order? }` | 新增，回 `201` + `Location` |
| GET | `/api/todos/{uuid}` | — | 單一 todo |
| PUT | `/api/todos/{uuid}` | `{ title?, description?, done?, due_at?, sort_order? }` | 局部更新，回 `200` |
| DELETE | `/api/todos/{uuid}` | — | 刪除，回 `204` |

全部需要 `Authorization: Bearer <access_token>`，亦收 `rqark-` API key。

`GET /api/todos` 排序：`done` 升序（未完成先）→ `sort_order` → `created_at`。

`?done=true` / `?done=false` 只回一半清單；其他值回 `400`。唔帶就回全部。

GET 回應：

```json
[
  {
    "id": "5c9d3b1e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
    "title": "寫 issue",
    "description": "",
    "done": false,
    "done_at": null,
    "due_at": "2026-10-20T09:00:00Z",
    "sort_order": 0,
    "created_at": "2026-10-16T11:30:00Z",
    "updated_at": "2026-10-16T11:30:00Z"
  }
]
```

## 規則

- **`title` 必填** —— 寫入前 trim（頭尾空白），trim 完係空字串回 `400`；長度上限 255。
- **`description` 上限 8192** —— 欄位係 text，上限只係防止一行當 bulk storage 用。
- **`done_at` 唔收** —— request 帶咗都忽略，一律由 `done` 推導（見上）。
- **`due_at` 三態** —— `PUT` 唔帶 `due_at` = 唔改；帶 `null` = 清；帶時間 = 設定。`created_at` / `updated_at` / `id` / `user_id` 由 server 管。
- **`done` 由 false 轉 true 才寫 `done_at`** —— 已經 done 再 POST 一次 `done: true` 唔會推遲原本嘅完成時間。
- **POST 一定係未完成** —— `done` 同 `done_at` 唔可以由 create 帶入。
- **唔分頁** —— 回 array，同 `/api/agents`、`/api/user-config` 一致。
- **跨用戶** —— 所有 query 都帶 `user_id = <token 用戶>`；`GET` / `PUT` / `DELETE` 一個唔存在或唔屬於自己嘅 `uuid` 一律回 `404`（唔回 `403`）。
- 回應唔含 `user_id`。

## 錯誤格式

```json
{ "error": { "code": "BAD_REQUEST", "message": "..." } }
```

| 場景 | HTTP | code |
|---|---|---|
| 缺 token / token 或 API key 無效 | 401 | `UNAUTHORIZED` |
| `title` 空或過長、`description` 過長、`done` query 唔係 bool、`uuid` 唔係 UUID、body 唔係合法 JSON | 400 | `BAD_REQUEST` |
| `uuid` 唔存在或屬其他用戶 | 404 | `NOT_FOUND` |
| 其他內部錯誤 | 500 | `INTERNAL` |
