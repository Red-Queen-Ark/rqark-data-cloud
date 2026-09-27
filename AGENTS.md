# 行動守則
1. 只做要求嘅事。做多咗就係錯，唔係加分。
6. 唔准留未完成標記（TBD、「等確認」）喺交付物。
7. 總需精簡回答問題。
8. 唔准講「你講得對」「收到」「好問題」。
9. 唔准用嚴重字眼，除非講得出觸發條件同後果。
10. 唔知就講唔知，唔准估。
11. 做咗就要 show 證據，唔准口頭報「已完成」。
12. 唔准問「要唔要我…」。

# 代碼守則
1. 改動代碼後運行 `go fmt`
2. 加或改 API（endpoint / swagger 註解）後運行 `go generate ./...`
3. 注解用英文寫，面向國際
4. `.env` 係默認配置，跟 repo 入 git；唔准加落 `.gitignore`，亦唔准當佢未追蹤

# API 做法
1. **用戶自己嘅數據集合唔分頁** —— `agents` / `providers` / `sessions`（連 `/sessions/{uuid}/messages`）一律 `GET` 直接回 JSON array，唔收 `?limit=&offset=`。呢啲 endpoint 係俾 app 當「雲端版本地檔」同步，一次過攞齊先可以對住本地檔案做覆寫。
2. **管理向清單先分頁** —— `api-keys` / `users` / `groups` 出 `utils.Page`（`{ items, total, limit, offset }`），因為佢哋冇本地對應物，亦會隨時間無限增長。
3. 單一資源出 `GET /{uuid}`，回同一個 DTO（`api-keys` 例外：列表已經有齊要顯示嘅欄位）；跨用戶一律 `404`（唔回 `403`）。
