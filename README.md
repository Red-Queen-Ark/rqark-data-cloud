# rqark-data-cloud
## Overview
負責帳號系統，同時按帳號保存 rqark 應用程式 產生的數據

gin + gorm + go-redis + gorm postgres driver + godotenv

## Quick Start
```
docker compose up -d
```

## API docs
The service describes itself: the Swagger document is served at `/api/swagger.json`
(and `/api/swagger.yml`), with Swagger UI at `/swagger/index.html`. On a default
checkout that is <http://localhost:7302/swagger/index.html>.

The document carries no `host` on purpose — every deployment answers on its own
address and port, so a host written into it would be wrong for all but one. Read
against the service it is served by, it resolves to that same origin.
