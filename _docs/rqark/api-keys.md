# Table

## api_keys

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK，即 Tracking ID |
| `user_id` | UUID | FK → `users.id`，index，擁有者 |
| `name` | string(255) | 預設 `''`，唔可以係空字串 |
| `key_prefix` | string(16) | key 嘅頭 12 個字，列表顯示用 |
| `key_hash` | string(64) | HMAC-SHA256 hex，unique index。明文永不入 DB |
| `is_active` | boolean | 預設 `true`，擁有者嘅開關 |
| `expires_at` | datetime (UTC) | nullable，`null` = 永不過期 |
| `last_used_at` | datetime (UTC) | nullable |
| `revoked_at` | datetime (UTC) | nullable |
| `revoked_message` | string(255) | nullable，enum：`user_deleted` / `expired` |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/api-keys` | — | 分頁列出自己嘅 key，`created_at` 新到舊 |
| POST | `/api/api-keys` | `{ "name": str, "expires_at"?: datetime }` | 建立 key，回 `201` + `Location` |
| PATCH | `/api/api-keys/{uuid}` | `{ "name"?, "is_active"?, "expires_at"? }` | 局部更新 |
| DELETE | `/api/api-keys/{uuid}` | — | 刪除 key，回 `204` |

冇 `GET /api/api-keys/{uuid}`：單一 key 冇細節頁，列表已經有齊要顯示嘅欄位。

全部需要 `Authorization: Bearer <access_token>`。呢四條 route **唔收 API key**（見「Vault」）。

GET / PATCH / DELETE 回應：

```json
{
  "id": "3f9a1c7e-1a1a-4a1a-8a1a-1a1a1a1a1a1a",
  "name": "laptop",
  "key_prefix": "rqark-wtYu6s",
  "is_active": true,
  "expires_at": null,
  "last_used_at": null,
  "revoked_at": null,
  "revoked_message": null,
  "created_at": "2026-09-25T15:16:11Z",
  "updated_at": "2026-09-25T15:16:11Z"
}
```

POST 回應同上，另加 `key`：

```json
"key": "rqark-wtYu6sUlsWGD4znp_ypZ2Mo0ZIb_XeXIJtd0RM1avhU"
```

`key` 淨係喺 POST 出現一次。

# Key 格式

```
rqark-<base64url(32 bytes crypto-random)>
```

`rqark-` 6 字 + 43 字 = 49 字元。前綴用嚟分辨憑證類型（分流去 API key 定 JWT），亦係 DB 反查嘅入口判斷。

`key_hash = HMAC-SHA256(SECRET_KEY, "rqark-apikey-v1:" + key)`，hex 成 64 字。

用 HMAC 而唔用裸 SHA-256：DB 單獨洩漏（backup、replica）唔可以離線撞 key，要有 `SECRET_KEY` 先算得出。domain prefix 令同一條 `SECRET_KEY` 喺其他地方簽出嘅值唔會撞。

用 HMAC 而唔用 bcrypt：驗證要 `WHERE key_hash = ?` 食 unique index 做 O(1) 反查；bcrypt 冇得 index，每次 request 要全表掃。

`key_prefix` 係明文頭 12 字，唔係秘密，純粹俾人喺列表分得出邊把打邊把。`rqark-` 後面用 base64url 字母表，所以有機會出 `rqark--xxxx`（外觀問題，唔影響反查）。

明文唔入 DB = 冇得睇返、冇得重設。唔見咗就 `DELETE` 舊嗰把再 `POST` 新嗰把。

# 認證

`Authorization: Bearer <token>` 由前綴分流：`rqark-` 開頭當 API key，其餘當 access token（JWT）。兩條路最後都放同一個 user 入 request context，所以同一條 route 兩者通吃。

**收 API key：**

| Path | 方法 |
|---|---|
| `/api/agents` | 全部 |
| `/api/providers` | 全部 |
| `/api/groups` | `GET` 列表、`GET /{uuid}` |
| `/api/users/me`、`/api/users/me/password` | `GET`、`PATCH`、`PUT` |

**唔收 API key（只收 access token）：**

| Path | |
|---|---|
| `/api/api-keys` | 見「Vault」 |
| `/api/users` 系列（superuser）、`/api/users/{uuid}` 系列 | 管理員操作 |
| `/api/groups` 寫入、`/api/groups/{uuid}/users` | 管理員操作 |
| `/api/auth/*` | 登入 / refresh |

## 規則

### 驗證

每個帶 API key 嘅 request 都要過齊四項，全部即時查 DB（冇 cache）：

1. 擁有者 `users.is_active = true`
2. `revoked_at IS NULL`
3. `api_keys.is_active = true`
4. `expires_at IS NULL` 或 `now < expires_at`

任何一項唔過都回同一個 `401`、`code: "UNAUTHORIZED"`、`message: "Invalid or revoked API key."`。未知嘅 key、熄咗嘅 key、停用用戶嘅 key 反應一樣，唔透露係邊一關死。

唔落 Redis cache：revoke 要即刻生效，cache 要處理失效。代價係每個 request 多一個 unique index 反查 + 一個 PK lookup。

### 狀態

| 情況 | `revoked_message` | 列表 | 用得 | PATCH |
|---|---|---|---|---|
| 正常 | `null` | 顯示 | 得 | 得 |
| 熄掣 | `null`（`is_active=false`） | 顯示 | 唔得 | 開返 `is_active: true` |
| 過期 | `expired` | 顯示 | 唔得 | 改 `expires_at` 續期 |
| 刪除 | `user_deleted` | 唔顯示 | 唔得 | `409` |

`is_active` 係開關，唔寫 `revoked_at`，可逆。

`expires_at` 係過期嘅依據；`revoked_at` / `revoked_message` 係過期嗰刻補寫嘅記錄。所以改一個未來嘅 `expires_at`（或 `null`）就係續期：`revoked_at` / `revoked_message` 清空、`is_active` 返 `true`、即刻用返得。

`revoked_at` 一寫落去就冇得清（除咗上面嗰個過期被續期嘅情況）。已刪嘅 key 任何 PATCH 都回 `409`、`code: "CONFLICT"`。

過期係 lazy 寫入：`revoked_at` / `revoked_message` 淨係喺兩個時機補寫 —— 有人拎住把 key 打 API，或者擁有者開 `GET /api/api-keys`。冇人碰過嘅過期 key，`revoked_at` 一直係 `null`。

### PATCH 細節

- `is_active: true` 打落一把 lapse 咗嘅 key 回 `409`，message 叫你改 `expires_at`。唯一續期入口係改 `expires_at`。
- 續期會順手 `is_active = true`。唔想開返就同一個 PATCH 帶 `{ "expires_at": "<未來>", "is_active": false }`，`is_active` 蓋過續期嘅預設。
- 刪一把已過期嘅 key，`revoked_message` 由 `expired` 變 `user_deleted`，`revoked_at` 更新成刪除時刻。

### 列表

- 排序 `created_at DESC, id`。
- `user_deleted` 嘅 key 唔出現，`total` 都唔計。過期嘅照出現。
- `GET` 之前會先幫嗰個用戶補寫 lapse 咗嘅 key。

### `last_used_at`

淨係喺 `last_used_at IS NULL OR last_used_at < now - 60s` 先寫，避免每個 request 一個寫入。精度一分鐘。

### 其他

- `name` 必填、上限 255，超咗或空回 `400`。冇 unique，同名可以有多把。
- `expires_at` 要喺未來，過去或等於現在回 `400`、`message: "expires_at must be in the future."`。
- 跨用戶：任何 query 都帶 `user_id = <token 用戶>`，用其他用戶嘅 `uuid` 回 `404`。
- 回應唔含 `user_id`，同 `agents.md` / `providers.md` 一致。
- 冇 device binding，同一把 key 可以多裝置共用。

## Vault

`/api/api-keys` 只認 access token。一把洩漏嘅 API key 唔可以續自己命、唔可以 mint 新 key、唔可以刪走擁有者其他 key；要收返控制權一定要經 JWT 登入。

即係 API key 用嚟打其他 API，唔用嚟管理自己。

## Pagination

```json
{ "items": [], "total": 128, "limit": 20, "offset": 0 }
```

`limit` default 20、上限 100。

## 同 rqark-app 嘅關係

`rqark-app` 用一條 `rqark-...` key 連上 data-cloud，拎返自己嘅 config 同 session 數據。key 同 cloud base URL 放喺 app 嘅 config dir（`~/.rqark`），所以 app 唔使經 login / refresh，亦唔使存密碼。

| app 要嘅嘢 | 打 | 對應本地 |
|---|---|---|
| agents（prompts / tools） | `/api/agents` | `~/.rqark/agents/*.yml` |
| providers | `/api/providers` | `~/.rqark/providers.yml` |
| 帳戶 | `/api/users/me` | 驗 key 通唔通 |
| session 數據 | 跟 `sessions.md` | app 嘅 `SessionStorage` |

讀同寫同一組 endpoint，所以同步係雙向。key 唔綁裝置，唔記 IP、唔認 UA，見到合法 key 就用得；外流就 `DELETE` 嗰把。
