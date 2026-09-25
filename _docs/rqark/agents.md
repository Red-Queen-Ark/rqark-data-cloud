# Table

## agents

| 欄位 | 型別 | 備註 |
|---|---|---|
| `id` | UUID | PK |
| `user_id` | UUID | FK → `users.id`，index，擁有者 |
| `name` | string(255) | 同 `user_id` 夾埋 unique |
| `description` | text | 預設 `''` |
| `prompts` | jsonb | 預設 `'[]'`，`[{ "name": str, "params"?: object }]` |
| `tools` | jsonb | 預設 `'[]'`，`[{ "name": str, "params"?: object }]`；`SubAgent` 嘅 `params` 再嵌 `prompts` / `tools` |
| `created_at` | datetime (UTC) | |
| `updated_at` | datetime (UTC) | |

`prompts` / `tools` 兩個 jsonb column 直接存 rqark-app `~/.rqark/agents/*.yml` 嘅對應欄位，形狀即 `BaseAgent.to_dict()` 出嘅 JSON（下面係完整一個 agent config 做例）：

```yaml
name: Angel Holiday
description: ''
prompts:
  - name: AgentsMdPrompt
  - name: DateTimePrompt
    params:
      timezone_name: Asia/Hong_Kong
tools:
  - name: WebFetch
  - name: SubAgent
    params:
      prompts:
        - name: DateTimePrompt
          params:
            timezone_name: Asia/Hong_Kong
      tools:
        - name: WebSearch
```

# API

| Method | Path | Request body | 說明 |
|---|---|---|---|
| GET | `/api/agents` | — | 列出自己全部 agent，`name` 升序 |
| GET | `/api/agents/{uuid}` | — | 取得單一 agent |
| POST | `/api/agents` | `{ "name": str, "description"?: str, "prompts"?: [...], "tools"?: [...] }` | 建立 agent，回 `201` + `Location` |
| PUT | `/api/agents/{uuid}` | 同 POST，未帶入的欄位不變更 | 更新 agent |
| DELETE | `/api/agents/{uuid}` | — | 刪除 agent，回 `204` |

GET / POST / PUT 回應即 `agents` row 減 `user_id`，另加 `id`、`created_at`、`updated_at`；`prompts` / `tools` 原樣（即 app `BaseAgent.to_dict()` 出嚟嘅 JSON）。

`GET /api/agents` 唔分頁，直接回一個 array（同 `GET /api/providers` 一致：一個用戶嘅 agent 數量少，app 亦要一次過攞齊）。

全部需要 `Authorization: Bearer <access_token>`。

## 規則

- **`(user_id, name)` unique** —— app 用 `name` 做 agent 標識（`f'{agent.name}:{session.uuid}'`）同 config 檔名（空格轉 `_`），同名會互相覆蓋。撞名回 `409`、`code: "CONFLICT"`。`name` 唔可以係空字串。
- **`prompts` / `tools` 係 opaque** —— cloud 唔解讀、唔驗證 `name`，`params` 嘅形狀由 rqark-core 嘅 prompt / tool class 決定（例如 `DateTimePrompt.timezone_name`、`CharacterPrompt.identity`、`SubAgent.params.prompts`）。cloud 唔維護呢份清單，避免同 rqark-core 版本綁死：core 加 prompt / tool 唔使改 cloud schema。
- **`tools` 嘅 `params` 可能帶憑證** —— `Gitea` / `GitHub` / `GitLab` 嘅 `params` 有 `token` / `username` / `base_url`。同 `providers.api_key` 一樣，讀寫一律只限擁有者。
- **冇內容嘅 `params` 唔存** —— 對應 app `_without_empty_params`，所以無參數嘅 item 寫 `{ "name": "WebFetch" }`，唔會出 `"params": {}`。
- **次序** —— `prompts` 次序唔重要（`AgentCore._get_system_prompts` 會按 `BasePrompt.priority` 由細到大排），`tools` 次序保留（`AgentCore` 順序 iterate 出 tool schema 同 tool prompt）。
- **跨用戶** —— 任何 query 都帶 `user_id = <token 用戶>`；用其他用戶嘅 `uuid` 一律回 `404`（唔回 `403`，避免枚舉出 id）。
