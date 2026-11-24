import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const script = fileURLToPath(new URL('../import', import.meta.url));
const token = 'import-test-token';

const ghRepo = (fullName, extra = {}) => ({
  full_name: fullName, name: fullName.split('/')[1], owner: { login: fullName.split('/')[0] },
  private: false, visibility: 'public', updated_at: '2026-01-01T00:00:00Z', ...extra,
});

// One server plays the gitsion API and the GitHub API, and records every call.
async function startWorld(t, { tracked = [], profiles = {}, renamed = {}, deleteStatus = {},
  githubStatus = 0 } = {}) {
  const state = [...tracked];
  const calls = [];
  const indexOf = (fullName) =>
    state.findIndex((name) => name.toLowerCase() === fullName.toLowerCase());
  const json = (res, status, body, headers = {}) => {
    res.writeHead(status, { 'Content-Type': 'application/json', ...headers });
    res.end(JSON.stringify(body));
  };
  const fail = (res, status, code) => json(res, status, { error: { code, message: code } });

  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, `http://${req.headers.host}`);
    const parts = url.pathname.split('/').filter(Boolean);
    const admin = req.headers.authorization === `Bearer ${token}`;
    calls.push(`${req.method} ${url.pathname}${url.search}`);

    if (url.pathname === '/api/v1/repos' && req.method === 'GET') {
      return json(res, 200, { repositories: state.map((fullName) => ({ fullName })) });
    }
    if (url.pathname === '/api/v1/repos' && req.method === 'POST') {
      if (!admin) return fail(res, 401, 'unauthorized');
      let body = '';
      for await (const chunk of req) body += chunk;
      const requested = JSON.parse(body).repository;
      // Like the real backend, a renamed repository is stored under its current name.
      const fullName = renamed[requested] ?? requested;
      if (indexOf(fullName) >= 0) return fail(res, 409, 'already_tracked');
      state.push(fullName);
      return json(res, 201, { fullName });
    }
    if (url.pathname.startsWith('/api/v1/repos/') && parts.length === 5
      && req.method === 'DELETE') {
      if (!admin) return fail(res, 401, 'unauthorized');
      const fullName = `${parts[3]}/${parts[4]}`;
      if (deleteStatus[fullName]) return fail(res, deleteStatus[fullName], 'internal_error');
      if (indexOf(fullName) >= 0) state.splice(indexOf(fullName), 1);
      return res.writeHead(204).end();
    }

    if (parts[0] === 'users' && parts.length === 2 && profiles[parts[1]]) {
      return json(res, 200, { login: parts[1], type: profiles[parts[1]].type });
    }
    if (['users', 'orgs'].includes(parts[0]) && parts[2] === 'repos' && profiles[parts[1]]) {
      const perPage = Number(url.searchParams.get('per_page'));
      const page = Number(url.searchParams.get('page') || 1);
      const all = profiles[parts[1]].repos;
      const next = new URL(url);
      next.searchParams.set('page', String(page + 1));
      return json(res, 200, all.slice((page - 1) * perPage, page * perPage),
        page * perPage < all.length ? { Link: `<${next}>; rel="next"` } : {});
    }
    if (parts[0] === 'repos' && parts.length === 3) {
      if (githubStatus) return json(res, githubStatus, { message: 'unavailable' });
      const fullName = `${parts[1]}/${parts[2]}`;
      // GitHub answers a former name with a redirect to the repository id.
      if (renamed[fullName]) {
        const location = `/repositories/${encodeURIComponent(renamed[fullName])}`;
        return res.writeHead(301, { Location: location }).end();
      }
      return json(res, 200, { full_name: fullName });
    }
    if (parts[0] === 'repositories' && parts.length === 2) {
      return json(res, 200, { full_name: decodeURIComponent(parts[1]) });
    }
    return json(res, 404, { message: 'Not Found' });
  });

  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => server.close());
  const matching = (method) => calls.filter((call) => call.startsWith(`${method} `));
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    calls,
    tracked: state,
    deleted: () => matching('DELETE').map((call) => call.slice('DELETE /api/v1/repos/'.length)),
    posted: () => matching('POST'),
  };
}

// Runs ./import without a terminal, away from any .env file.
async function run(args, world, env = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'gitsion-import-'));
  const cwd = path.join(root, 'work');
  await fs.mkdir(cwd);
  try {
    const child = spawn(process.execPath, [script, ...args], {
      cwd,
      env: { PATH: process.env.PATH, BACKEND_URL: world.url, GITHUB_API_URL: world.url,
        ADMIN_TOKEN: token, NO_COLOR: '1', ...env },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    child.stdout.setEncoding('utf8').on('data', (chunk) => { stdout += chunk; });
    child.stderr.setEncoding('utf8').on('data', (chunk) => { stderr += chunk; });
    const [code] = await once(child, 'close');
    return { code, stdout, stderr };
  } finally {
    await fs.rm(root, { recursive: true, force: true });
  }
}

test('--prune untracks what a JSON list does not name, whoever owns it', async (t) => {
  const world = await startWorld(t, { tracked: ['acme/keep', 'Acme/Gone', 'other/gone'] });
  const result = await run(
    ['--json', '["ACME/keep", "acme/fresh"]', '--all', '--prune', '--yes'], world);

  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(world.deleted(), ['Acme/Gone', 'other/gone']);
  assert.deepEqual(world.tracked, ['acme/keep', 'acme/fresh']);
  assert.match(result.stdout, /Done: 1 imported, 1 skipped\./);
  assert.match(result.stdout, /inline JSON does not list 2 of 3 tracked repositories/);
  assert.match(result.stdout, /Done: 2 untracked\./);
});

test('without --prune nothing is ever untracked', async (t) => {
  const world = await startWorld(t, { tracked: ['acme/keep', 'acme/gone'] });
  const result = await run(['--json', '["acme/keep"]', '--all'], world);

  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(world.calls, ['GET /api/v1/repos']);
  assert.doesNotMatch(result.stdout, /prune/i);
});

test('a profile only prunes the repositories it owns', async (t) => {
  const repos = Array.from({ length: 150 }, (_, index) => ghRepo(`acme/repo-${index}`));
  repos[149].updated_at = '2026-06-01T00:00:00Z';
  repos.push(ghRepo('acme/secret', { private: true, visibility: 'private' }));
  const world = await startWorld(t, {
    // repo-140 is on the second page; the listing reports secret as private.
    tracked: ['acme/repo-140', 'acme/secret', 'ACME/gone', 'acme-labs/thing', 'other/thing'],
    profiles: { acme: { type: 'Organization', repos } },
  });
  const result = await run(['@acme', '--select', 'none', '--prune', '--yes'], world);

  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(world.deleted(), ['ACME/gone']);
  assert.match(result.stdout,
    /acme \(Organization\) does not list 1 of 3 tracked repositories owned by acme/);
  // Pages are read in creation order, yet the table leads with the latest update.
  assert.ok(world.calls.some((call) => call.includes('sort=created&direction=asc')));
  const latest = result.stdout.indexOf('acme/repo-149');
  assert.ok(latest >= 0 && latest < result.stdout.indexOf('acme/repo-0 '));
});

test('a repository listed under a former name is kept', async (t) => {
  const world = await startWorld(t, {
    tracked: ['acme/new', 'acme/gone'],
    renamed: { 'acme/old': 'acme/new' },
  });
  const result = await run(['--json', '["acme/old"]', '--all', '--prune', '--yes'], world);

  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(world.deleted(), ['acme/gone']);
  assert.match(result.stdout,
    /Keeping acme\/new: it is listed under its former name acme\/old\./);
});

test('nothing is pruned when a rename cannot be checked', async (t) => {
  const world = await startWorld(t, { tracked: ['acme/a'], githubStatus: 500 });
  const result = await run(
    ['--json', '["acme/b"]', '--select', 'none', '--prune', '--yes'], world);

  assert.equal(result.code, 1);
  assert.deepEqual(world.deleted(), []);
  assert.match(result.stderr, /Could not check whether acme\/b was renamed, so nothing was pruned/);
});

test('--dry-run lists what --prune would untrack and changes nothing', async (t) => {
  const world = await startWorld(t, { tracked: ['acme/keep', 'acme/gone'] });
  const result = await run(
    ['--json', '["acme/keep", "acme/fresh"]', '--all', '--prune', '--dry-run'], world,
    { ADMIN_TOKEN: '' });

  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(world.deleted(), []);
  assert.deepEqual(world.posted(), []);
  assert.match(result.stdout, /\n1 {2}acme\/gone\n/);
  assert.match(result.stdout, /Dry run: would untrack 1 repository\./);
});

test('--prune needs --yes when there is no terminal to confirm on', async (t) => {
  const world = await startWorld(t, { tracked: ['acme/keep', 'acme/gone'] });
  const result = await run(['--json', '["acme/keep", "acme/fresh"]', '--all', '--prune'], world);

  assert.equal(result.code, 2);
  assert.deepEqual(world.calls, []);
  assert.match(result.stderr, /Cannot confirm --prune without a terminal\. Use --yes/);
});

test('--prune refuses a list with an entry it cannot read', async (t) => {
  const world = await startWorld(t, { tracked: ['acme/keep', 'acme/gone'] });
  const result = await run(
    ['--json', '["acme/keep", "https://github.com/acme/gone/tree/main"]', '--prune', '--yes'],
    world);

  assert.equal(result.code, 2);
  assert.deepEqual(world.calls, []);
  assert.match(result.stderr, /--prune needs the complete list, but 1 entry in inline JSON/);
});

test('an empty profile never prunes', async (t) => {
  const world = await startWorld(t, {
    tracked: ['acme/thing'],
    profiles: { acme: { type: 'User', repos: [] } },
  });
  const result = await run(['@acme', '--all', '--prune', '--yes'], world);

  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(world.deleted(), []);
  assert.match(result.stdout, /an empty source never untracks repositories/);
});

test('a failed untrack is reported and the rest still run', async (t) => {
  const world = await startWorld(t, {
    tracked: ['acme/a', 'acme/b', 'acme/keep'],
    deleteStatus: { 'acme/a': 500 },
  });
  const result = await run(['--json', '["acme/keep"]', '--prune', '--yes'], world);

  assert.equal(result.code, 1);
  assert.deepEqual(world.deleted(), ['acme/a', 'acme/b']);
  assert.deepEqual(world.tracked, ['acme/a', 'acme/keep']);
  assert.match(result.stderr, /failed {2}\[1\/2\] acme\/a: internal_error/);
  assert.match(result.stdout, /Done: 1 untracked, 1 failed\./);
});

test('pruning stops at a rejected admin token', async (t) => {
  const world = await startWorld(t, { tracked: ['acme/a', 'acme/b', 'acme/keep'] });
  const result = await run(['--json', '["acme/keep"]', '--prune', '--yes'], world,
    { ADMIN_TOKEN: 'wrong' });

  assert.equal(result.code, 1);
  assert.deepEqual(world.deleted(), ['acme/a']);
  assert.equal(world.tracked.length, 3);
  assert.match(result.stderr, /Stopping because the backend cannot continue\./);
});

test('--yes is only accepted together with --prune', async (t) => {
  const world = await startWorld(t);
  const result = await run(['--json', '["acme/keep"]', '--all', '--yes'], world);

  assert.equal(result.code, 2);
  assert.deepEqual(world.calls, []);
  assert.match(result.stderr, /--yes only applies to --prune\./);
});
