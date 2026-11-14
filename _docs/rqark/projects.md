# Table

## projects

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `user_id` | UUID | FK → `users.id`，`ON DELETE CASCADE`，擁有者 |
| `project_name` | string(64) | 顯示名，單一使用者內唯一 |
| `project_path` | string(512) | 相對路徑，相對於沙盒 volume 根目錄 `/workspace`；單一使用者內唯一 |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

唯一索引：`idx_projects_user_id_project_name`、`idx_projects_user_id_project_path`，兩者都以 `user_id` 為首欄。

`project_path` 只存容器內相對路徑，宿主機路徑不會進入此欄位。寫入時正規化（合併 `//`、去除尾部 `/`、去除 `.` 段），並拒絕：空白、開頭 `/`、任何 `..` 段、`\`、NUL、長度超過 512。

## sessions 新增欄位

| 欄位 | 型別 | 備註 |
|---|---|---|
| `project_id` | UUID | FK → `projects.id`，`ON DELETE SET NULL`，index，nullable |

`project_id` 為 `NULL` 代表 session 在 volume 根目錄 `/workspace` 工作，亦即加入 project 功能前的所有 session。刪除 project 時此欄位自動清空，session 與其 messages 保留；檔案不會被刪除，因為刪除資料夾不是 data-cloud 的能力。

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/projects` | — | 列出自己嘅 project，回 array，`updated_at` 新→舊 |
| POST | `/api/projects` | `{ "id"?: str, "project_name": str, "project_path": str }` | 建立 project，回 `201` + `Location` |
| GET | `/api/projects/{uuid}` | — | 取得單一 project |
| PUT | `/api/projects/{uuid}` | `{ "project_name"?: str, "project_path"?: str }`，未帶入的欄位不變更 | 更新 project |
| DELETE | `/api/projects/{uuid}` | — | 刪除 project，回 `204`；其 session 的 `project_id` 變 `null` |

回應即 row 減 `user_id` 與 `created_at` 以外無異：`{ "id", "project_name", "project_path", "created_at", "updated_at" }`。

全部需要 `Authorization: Bearer <access_token>`，亦收 `rqark-` API key（同 `/api/sessions` 一致：agent-cloud 用 key 存取）。清單**唔分頁**，直接回 array。

`sessions` 系列變更：

- 回應新增 `project_id`（`null` 代表未綁定）。
- `POST /api/sessions` 接受 `project_id`；必須為自己嘅 project，否則回 `400`。
- `PUT /api/sessions/{uuid}` 接受 `project_id`；未帶入不變更，明確帶 `null` 解除綁定。
- `GET /api/sessions?project_id=` 只取該 project 嘅 session，可與 `?source=` 併用。

## 規則

- **project 屬於單一使用者** —— 任何 query 都帶 `user_id = <token 用戶>`；用其他用戶嘅 `uuid` 一律回 `404`。
- **`project_id` 為他人 project 回 `400`** —— 與單一資源的 `404` 不同：create／update session 時呼叫方明確提出了該 id，回 `404` 會令「project 已刪除」與「不屬於你」無法區分。
- **重名、重路徑回 `409`** —— 同一個使用者下 project_name 或 project_path 重複，回 `409`、`code: "CONFLICT"`。
- **改 `project_path` 唔搬檔** —— 只係重新指向；使用該 project 的 session 於下一個 turn 改用新目錄。
- **`project_path` 唔會逃出 volume** —— 只接受相對路徑，`..` 與開頭 `/` 一律拒絕，所以不可能指向使用者 volume 以外。
