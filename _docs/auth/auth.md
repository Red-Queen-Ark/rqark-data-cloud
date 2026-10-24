# 認證系統

# API
| Method | Path | Request body | 說明 |
|---|---|---|---|
| POST | `/api/auth/login` | `{ "login": str, "password": str }` | 帳密換 token（login 接受 username 或 email） |
| POST | `/api/auth/refresh` | `{ "refresh_token": str }` | 換新 access + refresh，舊 refresh 即廢 |
| POST | `/api/auth/register` | `{ "username", "email", "password", "display_name" }` | 回 `201`，唔發 token。受 `ALLOW_SELF_REGISTRATION` 控制，預設關閉時回 `403` |
| GET | `/api/system/config` | — | 公開，回 `{ "allow_self_registration": bool }`。前端登入頁用它決定顯示唔顯示「自行註冊」 |
| POST | `/api/auth/forgot-password` | `{ "email": str }` | 回 `204`，唔話俾人知 email 存唔存在 |
| POST | `/api/auth/reset-password` | `{ "token": str, "new_password": str }` | 改密碼，唔撤銷任何 token |

**Auth transport 統一用 `Authorization: Bearer <access_token>` header，唔用 cookie。** refresh token 一律經 JSON body 帶。

### login

Request（JSON）：`{ "login": str, "password": str }`

`login` 接受 username 或 email，server 端判斷：
- 含 `@` → `WHERE email = ?`
- 否則 → `WHERE username = ?`

```json
{
  "access_token": "<jwt>",
  "refresh_token": "<opaque>"
}
```

**Login 失敗條件（統一回 `401`）：** 帳密錯、用戶唔存在、`is_active = false`。唔透露原因，避免 account enumeration。

### refresh

`refresh_token` 只可由 **JSON body** 帶，係 opaque token。驗證後 rotate（舊 token 即時失效）。

**Refresh flow：**
1. Hash token → `GETDEL auth:rt:{sha256(token)}` 攞返記錄（user_id）並即時消耗，舊 token 唔可以再用
2. DB SELECT load user by `user_id`（同 access token middleware 一致）
3. User 唔存在或 `is_active = false` → `401`
4. 發新 access + refresh

**Refresh 失敗條件（全部回 `401`）：** token 無效 / 過期 / 唔存在 / 已被 rotate / 用戶停用或不存在。


## 錯誤格式

```json
{ "error": { "code": "UNAUTHORIZED", "message": "Invalid username or password" } }
```

| 場景 | HTTP | code |
|---|---|---|
| 認證失敗（帳密錯 / 缺 token / refresh token 無效或過期） | 401 | `UNAUTHORIZED` |
| 授權失敗（權限不足） | 403 | `FORBIDDEN` |
| Reset token 無效 / 過期 / 已使用 / 密碼唔合規 | 400 | `BAD_REQUEST` |
| `username` / `email` / `name` 撞車 | 409 | `CONFLICT` |
| 會令 active superuser 變 0 嘅操作 | 409 | `LAST_SUPERUSER` |
| 停用自己 | 409 | `CONFLICT` |
| 其他內部錯誤 | 500 | `INTERNAL` |

## 設定

| 項目 | 設計決策 |
|---|---|
| 簽名演算法 | `HS256`，key = `SECRET_KEY`（最少 32 bytes，環境變數注入） |
| Access token 有效期 | 15 分鐘 |
| Refresh token 有效期 | 720 小時（30 日） |
| Token 讀取來源 | `Authorization: Bearer <token>` header |
| JWT subject | `sub` claim 存 user ID |
| Reset link | `{RESET_PASSWORD_URL}?token={token}`，`RESET_PASSWORD_URL` 由環境變數注入 |
| 自行註冊 | `ALLOW_SELF_REGISTRATION`，預設 `false`。內部部署由 admin 開帳號，所以未設都唔可以自己註冊 |

### `sub` claim

JWT payload 入面用 `sub` 存 user UUID（例如 `{"sub": "uuid"}`）。認證時從 claims 提取 `sub` 做 user lookup。`exp` 同簽發時間由 framework 管，唔可覆寫。

### 密碼政策

跟 Django `AUTH_PASSWORD_VALIDATORS` 四個預設 validator：

- `UserAttributeSimilarityValidator` —— 密碼唔可以同 `username` / `email` / `display_name` 太似
- `MinimumLengthValidator` —— 最少 8 字
- `CommonPasswordValidator` —— 唔可以係常見密碼
- `NumericPasswordValidator` —— 唔可以純數字

唔合規回 `400`、`code: "BAD_REQUEST"`（見上面錯誤格式表）。

## 用戶狀態檢查（is_active）

JWT 一旦簽發，內容唔會變。如果認證淨係驗 JWT signature，停用用戶手上嘅 access token 仲有最多 15 分鐘窗口可以照用。以下兩層防護確保停用即時生效：

### 第一層：Access token middleware

每個 authenticated request 都要做 DB lookup，唔可以淨靠 JWT signature：

1. 從 JWT claims 取 `sub`（user UUID）→ DB PK lookup load user → 放入 request context。
2. User 唔存在或 `is_active = false` → abort `401`（統一 envelope，`UNAUTHORIZED`）。
3. Route 嘅 superuser 檢查直接讀 context 入面嗰個 user，唔使再查 DB。

效果：停用嘅瞬間，所有 access token 即刻失效。每個 request 一次 PK SELECT，UUID 主鍵 + connection pool 成本可忽略。

### 第二層：Refresh 時再查一次

`/api/auth/refresh` 唔經 middleware，所以要自己再 load user 做 `is_active` 檢查（refresh flow step 2-3），停用用戶換唔到新 access token。

### 點解兩層都要

| 只做第一層 | 只做第二層 |
|---|---|
| 得一個防線，middleware 有 bug（例如漏掛某條 route、query 出錯時放行）就完全冇擋，access token 一路有效 | 停用前一剎那發出嘅 access token 喺 15 分鐘窗口內照用；15 分鐘後 refresh 換唔到先真正踢走 |

兩層一齊做，互相補底。

### 點解唔好喺 JWT 放 `is_active`

JWT 簽咗之後內容唔會更新。停用用戶，佢手上嗰個 JWT 入面 `is_active` 仲係 `true`，要等 next refresh 先更新 —— 但你唔想佢 refresh 到。所以一定要 DB lookup。

## Refresh token 儲存

Refresh token 儲存嘅設計決策：

1. **唔用明文 token 做 Redis key** —— 用 **SHA-256 hash** 做 key。Redis 一洩漏唔會令所有 refresh token 直接可讀。
2. **唔維護 per-user index** —— 冇「一次過撤銷某用戶所有 refresh token」呢個需求。入唔入得係由 `is_active` 決定（middleware + refresh 各查一次），改密碼唔會登出任何裝置。

### 儲存格式

```
# 單條 token：key = hash，value = user_id
SETEX  auth:rt:{sha256(refresh_token)}  {user_id}  TTL=RefreshTokenTimeout
```

收到明文 token 即刻 `sha256(token)` 先寫 Redis，讀取時一樣先 hash 先查。

呢個 key 帶 TTL，token 自然過期（或 client 棄用後到期）就自己消失，唔會累積。唔存在 per-user 集合，所以冇「淨加唔減」嘅膨脹問題。

### 單條 token 失效

Rotate 用 `GETDEL auth:rt:{sha256(token)}`，舊 token 讀一次就冇，唔可以重用。呢個係唯一令一條 refresh token 提早失效嘅方法。

停用用戶唔會刪 Redis 任何 key —— 停用係 DB 一個 `UPDATE users SET is_active = false WHERE id = ?`，之後 refresh flow step 3 同 middleware 都會擋。還原一樣係一個 `UPDATE`。

### 停用 / 還原

`DELETE /api/users/{uuid}`（軟刪）同 `PATCH /api/users/{uuid}` 帶 `is_active: false` / `true` 都係淨改 `users.is_active`，唔涉及 Redis，唔需要 transaction 排序。

註：改密碼（自己 / admin 重設 / reset-password）唔會令任何裝置登出，舊 refresh token 照用到 30 日 expiry 為止。

## Reset token

| 項目 | 值 |
|---|---|
| 格式 | 32 bytes crypto-random，hex encoding（64 chars） |
| 儲存 | `SETEX auth:reset:{sha256(token)} {user_id} TTL=1h` |
| Per-user index | `SETEX auth:reset:user:{user_id} {sha256(token)} TTL=1h` —— 舊 token 冇明文，冇呢個 index 就搵唔返舊 key 去刪。呢個 index 有 TTL 而且每用戶最多一個 key，唔會膨脹，同 refresh token 唔同 |
| Hash | SHA-256（同 refresh token 一致） |
| 一次性 | 驗證用 `GETDEL`（原子），消費後即冇，唔可重複用 |
| 每用戶同時只有一個 | 新 `forgot-password`：`GET auth:reset:user:{user_id}` → 有就 `DEL` 兩個 key → 再寫新嘅兩個 key，避免 token 堆積 |

`POST /api/auth/forgot-password` 流程：
1. 靜默查 email（唔論搵唔搵到都回 `204`）
2. 如果 user 存在 + `is_active = true`：`GETDEL`／`DEL` 舊 reset key → `SETEX` 新嘅兩個 key → 用 mailer 寄去 `{RESET_PASSWORD_URL}?token={token}`
3. 如果 user 唔存在或停用，跳過，照回 `204`

`POST /api/auth/reset-password` 流程：
1. Hash token → `GETDEL auth:reset:{sha256(token)}` 驗證兼消耗
2. `DEL auth:reset:user:{user_id}`
3. Update password
4. 回 `200`

改密碼唔會撤銷任何 refresh token，亦唔會令任何裝置登出（同 `PUT /api/users/me/password`、`PUT /api/users/{uuid}/password` 一致）。

**Reset token 失敗條件（全部回同一個 `400`）：** token 無效 / 過期 / 已使用。唔透露原因。

Step 1 已經消耗 token。如果 step 3 DB 失敗，用戶要重新走一次 `forgot-password` —— 呢個係刻意選擇（一次性優先於容錯）。
## 郵件（SMTP）

設定名跟 Django 的 `EMAIL_*`，所以熟悉 Django 的人不用重新學一套。

| 變數 | 預設 | 說明 |
|---|---|---|
| `EMAIL_BACKEND` | `console` | `console`（寫入 server log）、`smtp`（真正寄出）、`dummy`（丟棄） |
| `EMAIL_HOST` | 空 | `smtp` 必填，空就啟動失敗 |
| `EMAIL_PORT` | `25` | 587 配 `EMAIL_USE_TLS`，465 配 `EMAIL_USE_SSL` |
| `EMAIL_HOST_USER` | 空 | 空即不做 AUTH |
| `EMAIL_HOST_PASSWORD` | 空 | |
| `EMAIL_USE_TLS` | `false` | STARTTLS（先明文再升級） |
| `EMAIL_USE_SSL` | `false` | 一開始就是 TLS |
| `EMAIL_TIMEOUT` | `10` | 秒。Django 預設是無 timeout，這裡給 10 秒，免得一個卡住的 SMTP 拖住請求 |
| `DEFAULT_FROM_EMAIL` | `webmaster@localhost` | From |
| `RESET_PASSWORD_URL` | 空 | 重設頁的完整公開網址，`?token=` 由服務自己接上 |

**跟 Django 唯一不同的地方：預設 `EMAIL_BACKEND=console`，不是 `smtp`。** 沒有設定的 checkout 若照 Django 去連 `localhost:25`，`forgot-password` 只會每次寄信失敗。用 console 的話，連結會出現在 server log，開發與內網試用照樣走得通；正式部署把它設成 `smtp` 才算真正可用。

`EMAIL_USE_TLS` 與 `EMAIL_USE_SSL` 同時設 `true` 會在啟動時報錯（`log.Fatalf`），不會靜靜地選一個。

### 重設密碼信

- 主旨：`Password reset for {PROJECT_NAME}`。
- `multipart/alternative`：純文字 + 簡單 HTML（一個按鈕加一條後備網址）。
- 非 ASCII 的主旨用 RFC 2047 編碼，非 ASCII 的內文用 base64，避免依賴伺服器支援 8-bit。
- 模板在 `internal/service/templates/password_reset.{txt,html}`，用 `go:embed` 打包，執行時不需外部檔案。

### 寄信失敗怎麼辦

`forgot-password` 仍然回 `204`，錯誤只寫入 log。原因：這個 endpoint 對「email 存在」與「email 不存在」必須回同一個答案，若寄信失敗回 `500`，就等於用 500 洩漏哪些 email 存在。代價是寄不出去時使用者以為寄了 —— 所以 log 一定要有人看，或者直接看 SMTP 伺服器端。
