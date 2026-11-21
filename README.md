# rqark-data-cloud

The identity and storage service of the RQArk Ecology System: accounts, groups, API keys, and the data every rqark application keeps per account — agents, providers, sessions, projects, tasks, todos, memory and usage.

## Why rqark-data-cloud

Every rqark application works against files on its own machine. This is the copy that survives that machine: one account owns the data, and any application can read the same session back from anywhere.

- **Accounts are the source of identity** — login issues the tokens every other service checks. Nothing else in the ecosystem holds a password.
- **Provider credentials never leave here** — the chat endpoints proxy model calls with the stored credential, so a client can start a conversation without ever seeing the key.
- **The data is the sync target** — a user's collections come back whole rather than paged, so an application can overwrite its local copy with what it receives.

## The ecosystem

| Repository | What it is | Used here for |
| --- | --- | --- |
| [`rqark-core`](../rqark-core) | Stateless agent engine for Python. | Nothing; it runs behind the agent cloud. |
| [`rqark-app`](../rqark-app) | Sessions, providers and agents on top of `rqark-core`. | Its cloud client is the reference caller: sessions, agents, providers, projects, memory, SOPs and todos. |
| [`rqark-agent-cloud`](../rqark-agent-cloud) | HTTP and WebSocket service over `rqark-app`. | Nothing directly. |
| [`rqark-task`](../rqark-task) | Worker that runs scheduled agent tasks. | Claims and finalizes task runs over `/api/internal`. |
| `rqark-data-cloud` | This repository. | — |
| [`rqark-data-cloud-web-ui`](../rqark-data-cloud-web-ui) | Account and data management UI. | Calls this API. |

## Architecture

Go, with gin for routing, gorm over Postgres, and go-redis for refresh tokens and the task lease. Configuration is read from the environment once at startup, with `.env` as its default.

```
cmd/server/            the service: config, automigrate, routes, serve
cmd/createsuperuser/   the cold-start path for an empty installation
docker/                the service image; the binary is built argument-driven
internal/
├── api/               one file per resource, plus router.go and the swagger handlers
│   └── internal/      the claim and finalize endpoints, kept out of the spec
├── config/            environment settings, the database and the redis client
├── middlewares/       bearer auth, API keys, superuser, and the internal token
├── repository/        the gorm models and the queries over them
├── service/           the rules: what a resource may do and who may see it
├── utils/             passwords, tokens, paging and shared response types
└── version/           the build version, set at link time
_docs/                 the written documentation per resource
docs/                  the generated OpenAPI document
```

- **Layers** — `api` parses a request and answers; `service` decides; `repository` touches the database. A handler does not build a query, and a repository does not decide who is allowed.
- **Tables** — the server automigrates every model at startup. `createsuperuser` migrates only the account tables it writes, because it is meant to run against a database that has none.
- **Two ways in** — most routes accept either a bearer token or an API key; the account and key management routes accept only the token, so a leaked key cannot mint another one. The internal routes accept neither: they carry a shared secret in `X-Internal-Token`.

## Running

```bash
docker compose up -d
```

That builds the image and brings up Postgres, Redis and the server on `127.0.0.1:7302`. To run the binary on the host instead, start Postgres and Redis and run `go run ./cmd/server` — `.env` already points at both on localhost.

On an empty installation, create the first account with:

```bash
docker compose exec server createsuperuser
```

It writes the account straight to the database and asks for the password twice. Self-registration is off by default, so without it there is no way in.

## API

The service describes itself. The Swagger document is served at `/api/swagger.json` and `/api/swagger.yml`, with Swagger UI at `/swagger/index.html` — on a default checkout, <http://localhost:7302/swagger/index.html>.

The document carries no `host` on purpose: every deployment answers on its own address and port, and the service sits behind a proxy whose public name it does not know. Read against the service it is served by, it resolves to that same origin.

Changing an endpoint or a swagger comment means running `go generate ./...` from `cmd/server`, which rewrites `docs/`. That directory is committed.

## Environment

`.env` is part of the repository and holds the values a local checkout needs. Every key below falls back to the listed default when it is unset or empty.

| Variable | Default | Meaning |
| --- | --- | --- |
| `PROJECT_NAME` | empty | Prefix for the container, volume and network names compose creates. |
| `PORT` | empty | Port the server listens on; compose publishes it on `127.0.0.1`. |
| `POSTGRES_SERVER` | `localhost` | Postgres host. Compose overrides it to `pgsql`. |
| `POSTGRES_PORT` | `5432` | Postgres port. |
| `POSTGRES_DB` | `app` | Database name. |
| `POSTGRES_USER` | `postgres` | Database user. |
| `POSTGRES_PASSWORD` | empty | Database password. |
| `REDIS_SERVER` | `localhost` | Redis host. Compose overrides it to `redis`. |
| `REDIS_PORT` | `6379` | Redis port. |
| `SECRET_KEY` | empty | Signing key for access tokens. |
| `ALLOW_SELF_REGISTRATION` | `false` | Whether `POST /api/auth/register` is open. Off means an admin creates accounts. |
| `RESET_PASSWORD_URL` | empty | Full public address of the reset page; the token is appended as `?token=`. |
| `EMAIL_BACKEND` | `console` | `console` writes mail to the log, `smtp` delivers it, `dummy` drops it. |
| `EMAIL_HOST` | empty | Required when the backend is `smtp`. |
| `EMAIL_PORT` | `25` | 587 with `EMAIL_USE_TLS`, 465 with `EMAIL_USE_SSL`. |
| `EMAIL_HOST_USER` | empty | Empty means no AUTH. |
| `EMAIL_HOST_PASSWORD` | empty | |
| `EMAIL_USE_TLS` | `false` | STARTTLS. Setting it together with `EMAIL_USE_SSL` fails at startup. |
| `EMAIL_USE_SSL` | `false` | TLS from the first byte. |
| `EMAIL_TIMEOUT` | `10` | Seconds; a stuck SMTP server does not hold the request. |
| `DEFAULT_FROM_EMAIL` | `webmaster@localhost` | From header of every mail. |
| `TASK_AT_GRACE` | `300` | Seconds an `at` task may run late before it is recorded as missed. |
| `INTERNAL_TOKEN` | empty | Shared secret on `/api/internal`. Empty rejects every call, so the task workers stay idle. |
| `TASK_RUN_TIMEOUT` | `600` | Seconds one task run may take. |
| `TASK_RUN_TOKEN_BUFFER` | `120` | Added to the timeout to size the run token, so it outlives the run. |
| `TASK_RUN_STALE_AFTER` | timeout + buffer | Age at which a run still marked running is swept to lost. |

`EMAIL_BACKEND` defaults to `console` rather than `smtp` on purpose: a checkout that configured nothing would otherwise try `localhost:25` and fail every password reset. With console the link appears in the server log, which is enough for development and an internal trial.

## Testing

```bash
go test ./...
```

Tests that need a database read `TEST_POSTGRES_DSN` and skip when it is unset. The claim tests empty the task tables, so they only run against a database whose name ends in `_test`:

```bash
TEST_POSTGRES_DSN='postgres://admin:admin@localhost:5432/app_test?sslmode=disable' go test ./...
```

Run `go fmt` after a change. Comments are written in English, for an international reader.

## Deployment

`docker-compose.production.yml` is the deployment shape: the server, Postgres and Redis join the external `rqark-project` network under their service names, and no port is published — the reverse proxy in front decides what the public address is. Create the network once per host with `docker network create rqark-project`.

A `v*` tag builds `docker/dockerfile` and pushes the image to the GitHub Container Registry as both `X.Y.Z` and `X.Y`. The version string in the tag is passed to the binary, so `/api/version` reports the tag it was built from.

The image is an Alpine base with a static binary and an unprivileged user, listening on `7302`.

## Contributing

Issues, commits, pull requests, versions and releases follow [`CONTRIBUTING.md`](./CONTRIBUTING.md). Before opening a pull request, run `go fmt`, `go vet ./...` and `go test ./...`.
