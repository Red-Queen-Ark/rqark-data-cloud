# System Config

部署層設定，全 server 共用。即係 `user_configs` 冇 `user_id` 嗰版：一行設定對所有人都生效，所以 `key` 自己一個就 unique。

幾時用邊張表：設定屬於「呢個部署」—— 主色、預設語言、某個功能開唔開 —— 就用 `system_configs`；屬於「某個人」就用 `user_config`。

**唔好放秘密。** 任何登入用戶都讀得到全部，所以密碼、token、私鑰一律行環境變數。登入前就要知嘅設定亦唔應該放呢度（要向未登入嘅人公開嘅 flag 另外開一個 dedicated endpoint）。

# Table

## system_configs

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `key` | string(128) | unique，index，identity |
| `value` | jsonb | 設定值 |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

Unique index `(key)` —— 全 deployment 一個 key 得一筆。

`value` 用 jsonb：保留 type（`"dark"` / `true` / `24` / `{ ... }`），client 毋須自行約定格式。寫入原樣、讀回語意一樣（jsonb 會正規化 key 次序同空白）。

# API

| Method | Path | Request body | 權限 | 說明 |
|---|---|---|---|---|
| GET | `/api/system-config` | — | 任何登入用戶（access token 或 `rqark-` API key） | 回全部，array（唔分頁），`key` 升序 |
| PUT | `/api/system-config/{key}` | `{ "value": any }` | superuser（只收 access token） | 新增或覆寫一個 key（upsert），回 `200` |
| DELETE | `/api/system-config/{key}` | — | superuser（只收 access token） | 刪一個 key，回 `204` |

冇 `GET /api/system-config/{key}`：`GET /api/system-config` 已經回晒全部，單一 key 冇細節頁（同 `user_config`、`api-keys` 一致）。

GET 回應：

```json
[
  {
    "id": "3f9a1c7e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
    "key": "login_page_style",
    "value": { "style": "split" },
    "created_at": "2026-10-24T15:16:11Z",
    "updated_at": "2026-10-24T15:16:11Z"
  }
]
```

PUT 回單一 object（同 array 內嘅元素一樣），唔係 array。

## 規則

- **`key` 係 identity** —— unique。PUT 唔理個 key 存在未，一律 upsert：存在就改 `value`，唔存在就新增。
- **`key` 格式** —— 長度 1–128，只准 `[A-Za-z0-9_.-]`（URL-safe，可直接放 path）。唔合法回 `400`。
- **`value` 係任意 JSON 值** —— object / array / string / number / boolean 都得，原 type 保存。`null` 唔收（清一個 key 用 `DELETE`），回 `400`。
- **`value` 上限 64KB** —— 以編碼後 JSON 長度計；寫入前清 NUL（jsonb 收唔到 U+0000）。
- **`created_at` / `updated_at` 由 server 管** —— request 帶咗都忽略。
- **唔分頁** —— 回 array，同 `agents` / `providers` / `sessions` / `user-config` 一致：client 當佢係一份全域 config 檔，一次過攞齊。
- **讀開放、寫要 superuser** —— 一次寫入改動所有人見到嘅嘢，所以唔係任何登入帳號都做得。
- **同 `user_configs` 共用格式** —— key grammar、value 規則、size cap 全部住喺 `internal/service/config_value.go`，兩張表唔會各自漂移。
- 回應唔含任何 owner 欄位。

## 錯誤格式

```json
{ "error": { "code": "BAD_REQUEST", "message": "..." } }
```

| 場景 | HTTP | code |
|---|---|---|
| 缺 token / token 或 API key 無效 | 401 | `UNAUTHORIZED` |
| 已登入但唔係 superuser | 403 | `FORBIDDEN` |
| `key` 唔合法 / `value` 過大 / body 唔係合法 JSON | 400 | `BAD_REQUEST` |
| DELETE 一個唔存在嘅 key | 404 | `NOT_FOUND` |
| 其他內部錯誤 | 500 | `INTERNAL` |
