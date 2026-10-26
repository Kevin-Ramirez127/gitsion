# gitsion

A small self-hosted feed for the GitHub repositories you care about. Track a
repository once and see its **latest pushes, pull requests and issues** in one
newest-first stream.

The dashboard is intentionally public: there is no login. GitHub access uses the
public API by default; an optional personal access token raises the rate limit and
enables private repositories.

- **Backend:** Go, standard-library HTTP, MongoDB
- **Frontend:** Astro 7 with the Node adapter (SSR), no UI framework
- **Data:** GitHub REST API snapshots cached in MongoDB, refreshed in the background

## Features

- Track public (or private, with a token) repositories by `owner/name`, `github.com/owner/name` or a full URL.
- Activity feed of pushes, pull requests and issues across tracked repositories, newest-first inside each group, with actor avatars, labels, relative times and a show-all view.
- Project-oriented feed by default, grouping activity by repository; switch to a person-oriented view that groups the same activity by actor.
- Project groups collapse automatically: a project with no activity today shows only its newest activity, and any project with more than five activities shows the five newest. “Show more” expands the rest in place.
- Always-visible filter bar for activity type and issue labels, plus an owner filter. Links carry the active filters so they can be combined; filter changes update the dashboard in place without a full page reload.
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
| Owner | `?owner=acme` | Limits the feed and stats to one tracked owner. |
| Activity type | `?kind=push`, `?kind=pull`, `?kind=issue` | Repeat the parameter to select several kinds; omit it for all. |
| Issue label | `?label=bug` | Keeps only issue activities carrying any selected label. |
| Show all | `?show=all` | Removes the default 100-activity cap. |

Filters combine, for example `/?view=user&kind=issue&label=bug`. The
filter section is always visible above the feed; each chip shows how many
activities it matches and clears with a second click.

## Quick start (Docker)

```bash
cp .env.example .env      # optional; everything already works without it
docker compose up --build -d
```

Open <http://localhost:4321>.

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

The backend reads `.env` from the current directory or one level up (without
overriding real environment variables). `BACKEND_URL` defaults to
`http://localhost:8080` for the frontend.

### Environment

| Variable | Default | Purpose |
| --- | --- | --- |
| `GITHUB_TOKEN` | empty | Optional fine-grained PAT. Raises 60 → 5,000 requests/hour and allows private repos. |
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

A snapshot costs **4 requests** (repository, latest PR, tags, first issues page).
Without a token GitHub allows 60 requests/hour, so the backend defaults to a
30-minute cadence and warns at startup. That comfortably covers a handful of
public repositories. Add `GITHUB_TOKEN` for more repositories or a faster cadence.

## Bulk import

`./import` lists repositories from a GitHub organization/user profile and tracks
the ones you pick. It needs a running backend (`make backend` or
`docker compose up`) and Node 22+.

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
`BACKEND_URL`, `GITHUB_TOKEN` and `GITHUB_API_URL`. Without a token only public
repositories are listed. The backend also needs the token for the imported
snapshots, otherwise GitHub's 60 requests/hour limit applies to the import.

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

All routes are public JSON. The frontend calls this API server-side; form actions
are proxied through Astro so the browser never needs the backend URL.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness + MongoDB ping. |
| `GET` | `/api/v1/repos?owner=acme` | Tracked repositories (optional owner filter). |
| `POST` | `/api/v1/repos` | Track a repo: `{"repository":"owner/name"}`. |
| `GET` | `/api/v1/repos/{owner}/{name}` | Full cached snapshot. |
| `DELETE` | `/api/v1/repos/{owner}/{name}` | Stop tracking (idempotent, `204`). |
| `POST` | `/api/v1/repos/{owner}/{name}/refresh` | Fetch a fresh snapshot (20s cooldown). |
| `POST` | `/api/v1/sync` | Queue a full background sync (60s cooldown, `202`). |
| `GET` | `/api/v1/owners` | Owners derived from tracked repos, with counts. |

Example:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/repos \
  -H 'Content-Type: application/json' \
  -d '{"repository":"withastro/astro"}'
```

Errors use a consistent envelope:

```json
{"error":{"code":"repository_not_found","message":"..."}}
```

## How syncing works

- Tracking validates the repository against GitHub and stores the first snapshot immediately.
- A tracker goroutine syncs every tracked repository on the configured interval. Concurrent refreshes of the same repo are collapsed with `singleflight`.
- Each snapshot stores repository metadata, the latest updated PR (any state), up to 10 tags and up to 25 open issues (pull requests filtered out). Open-item count comes from GitHub and includes PRs.
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
  src/pages/              feed, detail page, form-action proxies
  src/components/         activity feed, forms, panels, tags, issues, icons
  src/lib/                typed API client and formatting helpers
  src/middleware.ts       security headers (CSP, nosniff, frame denial)
  public/                 favicon and confirmation script
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

- **No authentication.** Anyone who can reach the dashboard can add or remove tracked
  repositories. Put it behind a reverse proxy/VPN if that matters.
- **Private repositories are visible to everyone who can reach the dashboard.** The
  dashboard is public by design. A `GITHUB_TOKEN` is useful for rate limits, but only
  add private repositories when the whole deployment is in a trusted network.
- Only the repository owner/org is shown; collaborators would require more API calls/permissions.
- Tags are the first 10 returned by GitHub's tags endpoint; GitHub does not expose tag dates there.
- The feed is built from cached snapshots: one push timestamp per repository, the latest pull request per repository, and up to 25 open issues per repository. It defaults to the latest 100 activities, grouped by project; `?view=user` groups by actor and `?show=all` reveals the full feed.
- Issue lists show up to 25 open issues per repository (fewer if the first 100 issue/PR
  entries contain many PRs); the “open items” metric is GitHub's count and includes pull requests.
- No webhooks, charts, commit history or multi-user accounts. The API reads cached snapshots; background sync plus manual refresh keeps them current.
