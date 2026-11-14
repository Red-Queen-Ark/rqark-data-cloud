
# Rules
1. 改動代碼後運行 `go fmt`
2. 加或改 API（endpoint / swagger 註解）後運行 `go generate ./...`
3. 注解用英文寫，面向國際
4. `.env` 係默認配置，跟 repo 入 git；唔准加落 `.gitignore`，亦唔准當佢未追蹤

# API
1. **用戶自己嘅數據集合唔分頁** —— `agents` / `providers` / `sessions`（連 `/sessions/{uuid}/messages`）一律 `GET` 直接回 JSON array，唔收 `?limit=&offset=`。呢啲 endpoint 係俾 app 當「雲端版本地檔」同步，一次過攞齊先可以對住本地檔案做覆寫。
2. **管理向清單先分頁** —— `api-keys` / `users` / `groups` 出 `utils.Page`（`{ items, total, limit, offset }`），因為佢哋冇本地對應物，亦會隨時間無限增長。
3. 單一資源出 `GET /{uuid}`，回同一個 DTO（`api-keys` 例外：列表已經有齊要顯示嘅欄位）；跨用戶一律 `404`（唔回 `403`）。

# 發佈

- 所有 release 一律標 `prerelease`，唔准出穩定版。經 Gitea API 建 release 就寫 `"prerelease": true`，唔好預設 false。
- Release notes 唔准寫成「穩定」、「可用」或者對外承諾；只描述改咗乜。
