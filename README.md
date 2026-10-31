# gitsion

A small self-hosted feed for the GitHub repositories you care about. Track a
repository once and see its **latest pushes, pull requests and issues** in one
newest-first stream.

The dashboard is intentionally public: viewing requires no login and no account.
Only administrators can add, remove, refresh or sync repositories. The main feed
is read-only; repository management happens at `/admin`. Set `ADMIN_TOKEN` to
enable those actions; leave it empty and the instance is read-only. GitHub access
uses the public API by default; an optional personal access token raises the rate
limit. Private repositories are never tracked.

- **Backend:** Go, standard-library HTTP, MongoDB
- **Frontend:** Astro 7 with the Node adapter (SSR), no UI framework
- **Data:** GitHub REST API snapshots cached in MongoDB, refreshed in the background

## Features

- Track public repositories by `owner/name`, `github.com/owner/name` or a full URL.
- Public read-only dashboard with no viewer accounts; adding, removing, refreshing and syncing require an admin token and happen at `/admin`.
- Private repositories are rejected, even when `GITHUB_TOKEN` can see them.
- Activity feed of pushes, pull requests and issues across tracked repositories, newest-first inside each group, with actor avatars, labels, relative times and a show-all view.
- Project-oriented feed by default, grouping activity by repository; switch to a person-oriented view that groups the same activity by actor.
- Project groups collapse automatically: a project with no activity today shows only its newest activity, and any project with more than five activities shows the five newest. “Show more” expands the rest in place.
- Always-visible search bar that matches project names, owners and cached contributors. Search combines with the feed filters and updates the dashboard in place.
- Feed filters for activity type and issue labels, plus an owner filter. Both sections start collapsed and remember their open state across in-place updates. Links carry the active filters so they can be combined.
- Repository detail page with the latest PR (open/merged/closed), up to 10 tags, open issues with labels, owner card and repository facts.
- Background sync every 15 minutes with a token, 30 minutes without one. Manual per-repository refresh and “Sync all”.
- Failed GitHub syncs keep the previous good snapshot and show the error instead of blanking data.
- Empty, stale, 404 and backend-unreachable states are handled in the UI.
- Bulk-import every repository from a GitHub organization/user profile, or from a JSON list, with the interactive `./import` script.

## Feed views and filters

The dashboard groups the newest-first stream in one of two ways:

| View | Query | Groups by |
| --- | --- | --- |
| By project (default) | `/` | Repository, ordered by each repository's latest activity |
| By person | `/?view=user` | Actor, ordered by each actor's latest activity |

Inside a group, activities stay newest-first. The same filters work in both views:

| Filter | Query | Effect |
| --- | --- | --- |
| Search | `?q=astro` | Matches repository name, owner or a cached contributor login. Space-separated terms must all match. |
| Owner | `?owner=acme` | Limits the feed and stats to one tracked owner. |
| Activity type | `?kind=push`, `?kind=pull`, `?kind=issue` | Repeat the parameter to select several kinds; omit it for all. |
| Issue label | `?label=bug` | Keeps only issue activities carrying any selected label. |
| Show all | `?show=all` | Removes the default 100-activity cap. |

Filters combine, for example `/?q=astro&view=user&kind=issue&label=bug`. The
owners and filter sections start collapsed; the filter summary still shows the
matching activity count. Each chip shows how many activities it matches and
clears with a second click, and a search for a contributor also narrows the feed
to that contributor's repositories.

## Quick start (Docker)

```bash
cp .env.example .env      # optional; everything already works without it
# To manage repositories, generate an admin token and put it in .env:
#   openssl rand -base64 48
docker compose up --build -d
```

Open <http://localhost:4321> to read the feed. The main dashboard is read-only.
Without `ADMIN_TOKEN` the whole instance is read-only. To manage repositories,
open <http://localhost:4321/admin>, sign in with `ADMIN_TOKEN`, and add public
repositories or start a full sync there. A repository's detail page also offers
refresh and remove controls once you are signed in.

The compose stack runs three services:

| Service | Address | Notes |
| --- | --- | --- |
| frontend | `http://localhost:4321` | Public activity feed, SSR, proxies form actions |
| backend | `http://127.0.0.1:8080` | JSON API, bound to loopback on the host |
| mongo | `mongodb://127.0.0.1:27017` | Persistent `gitsion_mongo-data` volume |

Useful commands:

```bash
make help          # list targets
make up            # build + start the full stack
make logs          # follow logs
make down          # stop the stack
make test          # Go tests + Astro type check
```

## Local development

Prerequisites: Go 1.27+, Node 24+, Docker (for MongoDB).

```bash
cp .env.example .env
make mongo         # MongoDB on localhost:27017
make backend       # terminal 1: API on :8080
make frontend      # terminal 2: Astro on :4321
```

`make frontend` loads `../.env` through `astro.config.mjs`, so `ADMIN_TOKEN` is
available to both sides. Shell environment variables take precedence over the
file. In production, pass `ADMIN_TOKEN` and `SESSION_SECRET` to the runtime
container instead.

The backend reads `.env` from the current directory or one level up (without
overriding real environment variables). `BACKEND_URL` defaults to
`http://localhost:8080` for the frontend.

### Environment

| Variable | Default | Purpose |
| --- | --- | --- |
| `ADMIN_TOKEN` | empty | Enables adding/removing repositories and admin login. Empty = read-only. Generate with `openssl rand -base64 48`. |
| `SESSION_SECRET` | derived from `ADMIN_TOKEN` | Optional HMAC key for the admin session cookie. Sessions are also bound to `ADMIN_TOKEN`, so rotating either signs out all admins. |
| `GITHUB_TOKEN` | empty | Optional fine-grained PAT. Raises 60 → 5,000 requests/hour. Private repositories are not tracked. |
| `GITHUB_API_URL` | `https://api.github.com` | GitHub Enterprise or test server. |
| `SYNC_INTERVAL` | `15m` with token, `30m` without | Background refresh cadence. Minimum `1m`. |
| `SYNC_CONCURRENCY` | `3` | Repositories synced in parallel (1–10). |
| `MONGODB_URI` | `mongodb://localhost:27017` | MongoDB connection string. Compose overrides it. |
| `MONGODB_DB` | `gitsion` | Database name. |
| `PORT` | `8080` | Backend listen port. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `BACKEND_URL` | `http://localhost:8080` | Frontend → backend URL (server-side only). |
| `FRONTEND_PORT` | `4321` | Host port published by Docker Compose. |

### GitHub rate limits

A snapshot costs **5 requests** (repository, latest PR, tags, first issues page,
top contributors). Without a token GitHub allows 60 requests/hour, so the backend
defaults to a 30-minute cadence and warns at startup. That comfortably covers a handful of
public repositories. Add `GITHUB_TOKEN` for more repositories or a faster cadence.

## Bulk import

`./import` lists repositories from a GitHub organization/user profile and tracks
the ones you pick. It needs a running backend (`make backend` or
`docker compose up`) and Node 22+. Because imports are writes, `ADMIN_TOKEN` must
be set in the environment or `.env`; private and internal repositories are skipped.

```bash
./import https://github.com/withastro
./import https://github.com/orgs/astral-sh/repositories --dry-run
./import @withastro --select 1,3-5
./import repos.json --all
./import --json repos.json --all
./import --json '["withastro/astro", "astral-sh/ruff"]' --all

# Through make:
make import ARGS="https://github.com/withastro"
```

For a profile, the script first asks whether to import **all** repositories.
Answer no to choose individual numbers or ranges such as `1,3-5`. Already
tracked repositories are skipped. `--all` and `--select` make it
non-interactive; `--dry-run` only shows the table.

The script loads `.env` from the current directory or its parent and honors
`BACKEND_URL`, `ADMIN_TOKEN`, `GITHUB_TOKEN` and `GITHUB_API_URL`. Without a
GitHub token only public repositories are listed. The backend also needs the same
`ADMIN_TOKEN` for the imported snapshots, otherwise the API is read-only.

`--json` (or a positional path ending in `.json`) accepts an array of repository
strings or GitHub-like objects, or an inline array. A top-level
`{"repositories": [...]}` object also works:

```json
[
  "withastro/astro",
  "https://github.com/withastro/starlight",
  { "full_name": "astral-sh/ruff", "description": "An extremely fast Python linter" }
]
```

Run `./import --help` for every option.

## API

All read routes are public JSON. Mutating routes require
`Authorization: Bearer <ADMIN_TOKEN>`; without `ADMIN_TOKEN` they return
`403 writes_disabled`. The frontend calls this API server-side; form actions are
proxied through Astro so the browser never needs the backend URL or the token.

| Method | Path | Access | Description |
| --- | --- | --- | --- |
| `GET` | `/healthz` | public | Liveness + MongoDB ping. |
| `GET` | `/api/v1/repos?owner=acme` | public | Tracked repositories (optional owner filter). |
| `POST` | `/api/v1/repos` | admin | Track a public repo: `{"repository":"owner/name"}`. |
| `GET` | `/api/v1/repos/{owner}/{name}` | public | Full cached snapshot. |
| `DELETE` | `/api/v1/repos/{owner}/{name}` | admin | Stop tracking (idempotent, `204`). |
| `POST` | `/api/v1/repos/{owner}/{name}/refresh` | admin | Fetch a fresh snapshot (20s cooldown). |
| `POST` | `/api/v1/sync` | admin | Queue a full background sync (60s cooldown, `202`). |
| `GET` | `/api/v1/owners` | public | Owners derived from tracked repos, with counts. |
| `GET` | `/api/v1/admin/session` | admin | Verify an admin token (`204`). |

Example:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/repos \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"repository":"withastro/astro"}'
```

Errors use a consistent envelope:

```json
{"error":{"code":"repository_not_found","message":"..."}}
```

## Security

- **Public read, admin write.** No login is required to view the feed. `ADMIN_TOKEN` enables `/admin`, and the backend checks the same token on every mutation.
- **Read-only without a token.** If `ADMIN_TOKEN` is empty, the UI hides management controls and the API returns `403 writes_disabled`.
- **Only public repositories.** Tracking and refreshing reject repositories whose GitHub visibility is not `public`. Any private records left in an older database are hidden from the API and skipped by background sync.
- **Token handling.** The browser submits the token once to the Astro server, which sets an `HttpOnly`, `SameSite=Strict`, 12-hour signed cookie. The token is never rendered into HTML or exposed to client-side JavaScript.
- **Put the frontend behind HTTPS** in production. The admin cookie uses `Secure` when the request arrives over HTTPS or `X-Forwarded-Proto: https`.
- **Keep the backend private.** Docker Compose binds it to `127.0.0.1:8080`; only the frontend should be reachable from the network.
- **Rotate credentials deliberately.** Change `ADMIN_TOKEN` to replace the backend credential and sign out every existing admin session. Rotating `SESSION_SECRET` also invalidates sessions without changing the backend token.

## How syncing works

- Tracking validates the repository against GitHub, rejects non-public repositories, and stores the first snapshot immediately.
- A tracker goroutine syncs every tracked repository on the configured interval. Concurrent refreshes of the same repo are collapsed with `singleflight`.
- Each snapshot stores repository metadata, the latest updated PR (any state), up to 10 tags, up to 25 open issues (pull requests filtered out) and the top 100 contributors. Open-item count comes from GitHub and includes PRs.
- If a sync fails, the last successful snapshot remains visible while `syncError` and `lastSyncAttempt` are updated.
- MongoDB has a unique index on the normalized `owner/name`, so case variants cannot be tracked twice.

## Project layout

```
backend/
  cmd/server/             entrypoint, graceful shutdown
  internal/api/           routes, handlers, logging/recovery middleware
  internal/config/        env parsing and .env helper
  internal/github/        GitHub REST client + fixtures tests
  internal/model/         domain/BSON/JSON types
  internal/service/       track, untrack, refresh, background sync
  internal/store/         Mongo implementation + integration test
frontend/
  src/pages/              feed, detail page, admin login, form-action proxies
  src/components/         activity feed, forms, panels, tags, issues, icons
  src/lib/                typed API client, auth/session helper and formatting helpers
  src/middleware.ts       admin-session guard + security headers (CSP, nosniff, frame denial)
  public/                 favicon, confirmation and in-place feed scripts
import                    interactive bulk importer (Node, no dependencies)
docker-compose.yml
```

## Testing

```bash
make test
# or individually
cd backend && go test ./...
cd frontend && npm run check && npm run build
```

Mongo-backed store tests are opt-in:

```bash
cd backend
MONGODB_TEST_URI=mongodb://localhost:27017 \
  go test -tags=integration ./internal/store/...
```

## Notes and limits

- **Public dashboard, protected management.** Anyone who can reach the dashboard can read it. The main feed has no write controls; only holders of `ADMIN_TOKEN` can change the tracked list from `/admin` or a repository detail page.
- **Private repositories are not supported.** A `GITHUB_TOKEN` can raise the rate limit, but repositories whose visibility is not public are rejected and never shown.
- Only the repository owner/org and top contributors (up to 100, used for search) are stored; there is no full collaborator or permission model.
- Tags are the first 10 returned by GitHub's tags endpoint; GitHub does not expose tag dates there.
- The feed is built from cached snapshots: one push timestamp per repository, the latest pull request per repository, and up to 25 open issues per repository. It defaults to the latest 100 activities, grouped by project; `?view=user` groups by actor and `?show=all` reveals the full feed.
- Issue lists show up to 25 open issues per repository (fewer if the first 100 issue/PR
  entries contain many PRs); the “open items” metric is GitHub's count and includes pull requests.
- No webhooks, charts, commit history or multi-user accounts. The API reads cached snapshots; background sync plus manual refresh keeps them current.
