import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import http from 'node:http';
import test from 'node:test';
import { ADMIN_COOKIE, createAdminSession, SESSION_TTL_SECONDS } from '../../src/lib/auth.ts';

const token = 'integration-test-token';
const secret = 'integration-test-secret';
const user = (login) => ({ login, avatarUrl: `https://avatars.example/${login}`,
  htmlUrl: `https://github.com/${login}`, type: 'User' });
const newest = {
  number: 3, title: 'Merged by a maintainer', state: 'merged', draft: false,
  author: user('alice'), mergedBy: user('maintainer'), htmlUrl: 'https://github.com/acme/widget/pull/3',
  createdAt: '2026-02-01T00:00:00Z', updatedAt: '2026-02-04T00:00:00Z',
  mergedAt: '2026-02-04T00:00:00Z',
};
const unknown = { ...newest, number: 2, title: 'Merged without a known actor',
  mergedBy: null, htmlUrl: 'https://github.com/acme/widget/pull/2',
  mergedAt: '2026-02-03T00:00:00Z' };
const older = { ...newest, number: 1, title: 'Older open PR', state: 'open',
  mergedAt: null, mergedBy: null, author: user('bob'),
  htmlUrl: 'https://github.com/acme/widget/pull/1',
  updatedAt: '2026-02-01T00:00:00Z' };
const repo = {
  fullName: 'acme/widget', name: 'widget', owner: user('acme'),
  description: 'Integration fixture', htmlUrl: 'https://github.com/acme/widget',
  language: 'Go', defaultBranch: 'main', visibility: 'public', topics: [],
  stars: 0, forks: 0, openItems: 1, pushedAt: '', latestPr: newest,
  pullRequests: [newest, unknown, older], tags: [], issues: [], contributors: [], pushes: [],
  trackedAt: '2026-02-01T00:00:00Z', lastSyncedAt: '2026-02-04T00:00:00Z',
  lastSyncAttempt: '2026-02-04T00:00:00Z',
};
const mutations = {
  track: ['POST', '/api/v1/repos'],
  untrack: ['DELETE', '/api/v1/repos/acme/widget'],
  refresh: ['POST', '/api/v1/repos/acme/widget/refresh'],
  sync: ['POST', '/api/v1/sync'],
};

function aliases(route) {
  return [`/api/${route}`, `/api/${route}/`, `/api//${route}`,
    `/api/%${route.charCodeAt(0).toString(16)}${route.slice(1)}`];
}

async function listen(server) {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return server.address().port;
}

async function launchFrontend(backendPort, adminToken) {
  const probe = http.createServer();
  const port = await listen(probe);
  await new Promise((resolve) => probe.close(resolve));
  const base = `http://127.0.0.1:${port}`;
  const child = spawn(process.execPath, ['dist/server/entry.mjs'], {
    cwd: new URL('../../', import.meta.url),
    env: { ...process.env, HOST: '127.0.0.1', PORT: String(port),
      BACKEND_URL: `http://127.0.0.1:${backendPort}`, ADMIN_TOKEN: adminToken,
      SESSION_SECRET: secret, SITE_URL: base, ASTRO_TELEMETRY_DISABLED: '1' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let logs = '';
  child.stdout.on('data', (chunk) => { logs += chunk; });
  child.stderr.on('data', (chunk) => { logs += chunk; });
  const exited = once(child, 'exit');
  const stop = async () => {
    if (child.exitCode !== null || child.signalCode !== null) return;
    child.kill('SIGTERM');
    const timeout = setTimeout(() => child.kill('SIGKILL'), 3000);
    try { await exited; } finally { clearTimeout(timeout); }
  };
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(`${base}/healthz`, { signal: AbortSignal.timeout(500) })).ok) {
        return { base, stop };
      }
    } catch {}
    if (child.exitCode !== null) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  await stop();
  throw new Error(`Frontend did not start: ${logs}`);
}

function post(base, path, headers = {}) {
  return fetch(base + path, {
    method: 'POST', redirect: 'manual',
    headers: { Accept: 'application/json', ...headers },
    body: new URLSearchParams({ repository: 'acme/widget', owner: 'acme', name: 'widget' }),
    signal: AbortSignal.timeout(5000),
  });
}

test('built dashboard protects mutations and renders cached activity accurately', async (t) => {
  const previous = { token: process.env.ADMIN_TOKEN, secret: process.env.SESSION_SECRET };
  process.env.ADMIN_TOKEN = token;
  process.env.SESSION_SECRET = secret;
  const cookie = `${ADMIN_COOKIE}=${createAdminSession().value}`;
  const expired = `${ADMIN_COOKIE}=${createAdminSession(Date.now() - (SESSION_TTL_SECONDS + 60) * 1000).value}`;
  t.after(() => {
    if (previous.token === undefined) delete process.env.ADMIN_TOKEN;
    else process.env.ADMIN_TOKEN = previous.token;
    if (previous.secret === undefined) delete process.env.SESSION_SECRET;
    else process.env.SESSION_SECRET = previous.secret;
  });

  const calls = [];
  const backend = http.createServer((req, res) => {
    if (req.method !== 'GET') calls.push({ method: req.method, path: req.url,
      privileged: req.headers.authorization === `Bearer ${token}` });
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/api/v1/repos' && req.method === 'GET') {
      return res.end(JSON.stringify({ repositories: [repo] }));
    }
    if (req.url === '/api/v1/owners') {
      return res.end(JSON.stringify({ owners: [{ ...user('acme'), repoCount: 1 }] }));
    }
    if (req.url === '/api/v1/sync') {
      res.statusCode = 202;
      return res.end('{"status":"scheduled"}');
    }
    if (req.method === 'DELETE') { res.statusCode = 204; return res.end(); }
    if (req.method === 'POST' && req.url === '/api/v1/repos') res.statusCode = 201;
    res.end(JSON.stringify(repo));
  });
  const backendPort = await listen(backend);
  t.after(() => new Promise((resolve) => backend.close(resolve)));
  const app = await launchFrontend(backendPort, token);
  t.after(app.stop);

  await t.test('all mutation routes reject missing, invalid and expired sessions, including aliases', async () => {
    for (const route of Object.keys(mutations)) {
      for (const path of aliases(route)) {
        for (const session of ['', `${ADMIN_COOKIE}=invalid`, expired]) {
          const response = await post(app.base, path, { Cookie: session });
          assert.ok([401, 404].includes(response.status), `${path}: ${response.status}`);
        }
      }
      const html = await post(app.base, `/api/${route}/`, { Accept: 'text/html' });
      assert.equal(html.status, 303);
      assert.ok(html.headers.get('location').startsWith('/admin?error='));
      assert.equal(html.headers.get('x-frame-options'), 'DENY');
    }
    assert.deepEqual(calls, []);
  });

  await t.test('cross-site and same-site browser requests are denied even with a valid session', async () => {
    for (const route of Object.keys(mutations)) {
      for (const path of aliases(route)) {
        for (const site of ['cross-site', 'same-site']) {
          const response = await post(app.base, path, { Cookie: cookie, 'Sec-Fetch-Site': site });
          assert.ok([403, 404].includes(response.status), `${path}: ${response.status}`);
        }
      }
    }
    assert.deepEqual(calls, []);
  });

  await t.test('signed same-origin sessions still allow each administrative action', async () => {
    for (const [route, [method, path]] of Object.entries(mutations)) {
      for (const suffix of ['', '/']) {
        calls.length = 0;
        const response = await post(app.base, `/api/${route}${suffix}`, {
          Cookie: cookie, 'Sec-Fetch-Site': 'same-origin',
        });
        assert.equal(response.status, 303);
        assert.deepEqual(calls, [{ method, path, privileged: true }]);
      }
    }
    calls.length = 0;
  });

  await t.test('read-only mode rejects mutations and continues serving the public dashboard', async () => {
    const readOnly = await launchFrontend(backendPort, '');
    try {
      for (const route of Object.keys(mutations)) {
        for (const suffix of ['', '/']) {
          const response = await post(readOnly.base, `/api/${route}${suffix}`, { Cookie: cookie });
          assert.equal(response.status, 403);
          assert.equal((await response.json()).error.code, 'writes_disabled');
        }
      }
      assert.equal((await fetch(readOnly.base)).status, 200);
      assert.deepEqual(calls, []);
    } finally { await readOnly.stop(); }
  });

  await t.test('the dashboard counts older open PRs and renders known and unknown merge actors', async () => {
    const response = await fetch(app.base);
    assert.equal(response.status, 200);
    const home = await response.text();
    assert.ok(home.includes('aria-label="Repos with open PR: 1"'));
    assert.ok(home.includes('Older open PR'));
    const rows = [...home.matchAll(/<li class="feed-item">([\s\S]*?)<\/li>/g)].map(match => match[1]);
    const knownRow = rows.find(row => row.includes('Merged by a maintainer'));
    assert.ok(knownRow.includes('https://github.com/maintainer'));
    assert.ok(!knownRow.includes('https://github.com/alice'));
    const unknownRow = rows.find(row => row.includes('Merged without a known actor'));
    assert.ok(unknownRow.includes('Pull request merged'));
    assert.ok(!unknownRow.includes('feed-actor'));
    const people = await (await fetch(`${app.base}/?view=user`)).text();
    assert.ok(people.includes('Other activity'));
    assert.ok(!people.includes('https://github.com/alice'));
  });
});

test('repository ranking counts the full feed and filters exact repositories', async (t) => {
  const issue = (number, title, labels) => ({ number, title, labels, state: 'open',
    author: user('reporter'), htmlUrl: `https://github.com/acme/widget/issues/${number}`,
    createdAt: '2026-02-05T00:00:00Z', updatedAt: '2026-02-05T00:00:00Z', comments: 0 });
  function fixture(fullName, pushCount, issues = []) {
    const [owner, name] = fullName.split('/');
    return { ...repo, fullName, name, owner: user(owner), htmlUrl: `https://github.com/${fullName}`,
      latestPr: null, pullRequests: [], issues,
      pushes: Array.from({ length: pushCount }, (_, index) => ({
        id: index + 1, actor: user('pusher'), ref: 'refs/heads/main',
        before: '1'.repeat(40), after: '2'.repeat(40), timestamp: '2026-02-06T00:00:00Z', forced: false,
      })),
    };
  }
  let fixtures = [
    { ...fixture('acme/widget', 100, [issue(1, 'Widget bug', ['bug'])]),
      latestPr: newest, pullRequests: [newest, unknown, older] },
    fixture('acme/widget-tools', 5, [issue(2, 'Tools bug one', ['bug']),
      issue(3, 'Tools bug two', ['bug']), issue(4, 'Tools feature', ['feature'])]),
    fixture('acme/docs', 3, [issue(5, 'Docs bug', ['bug'])]),
    fixture('elsewhere/runner', 2),
    fixture('elsewhere/zeta', 1),
    fixture('elsewhere/alpha', 1),
    fixture('acme/inactive', 0),
  ];
  const backend = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://backend.test');
    res.setHeader('Content-Type', 'application/json');
    if (url.pathname === '/api/v1/repos') {
      const owner = url.searchParams.get('owner')?.toLowerCase();
      return res.end(JSON.stringify({ repositories: fixtures.filter(repository =>
        !owner || repository.owner.login.toLowerCase() === owner) }));
    }
    if (url.pathname === '/api/v1/owners') {
      const owners = new Map();
      for (const repository of fixtures) {
        const owner = owners.get(repository.owner.login) ?? { ...repository.owner, repoCount: 0 };
        owner.repoCount++;
        owners.set(owner.login, owner);
      }
      return res.end(JSON.stringify({ owners: [...owners.values()] }));
    }
    res.statusCode = 404;
    res.end('{}');
  });
  const backendPort = await listen(backend);
  t.after(() => new Promise(resolve => backend.close(resolve)));
  const app = await launchFrontend(backendPort, '');
  t.after(app.stop);

  async function page(path = '/') {
    const response = await fetch(new URL(path, app.base));
    assert.equal(response.status, 200);
    return response.text();
  }
  function rankingEntries(html) {
    const section = html.match(/<section\b[^>]*class="activity-ranking"[^>]*>([\s\S]*?)<\/section>/)?.[1] ?? '';
    return [...section.matchAll(/<a\b[^>]*class="activity-ranking-link[^\"]*"[^>]*>/g)].map(([tag]) => {
      const label = tag.match(/aria-label="(?:Filter feed by|Clear filter for) ([^\"]+), ([\d,]+) captured activit(?:y|ies)"/);
      assert.ok(label, `Missing accessible ranking count: ${tag}`);
      return { name: label[1], count: Number(label[2].replaceAll(',', '')),
        href: new URL(tag.match(/href="([^\"]+)"/)[1].replaceAll('&amp;', '&'), app.base),
        active: tag.includes('aria-current="true"') };
    });
  }
  const feedRows = html => [...html.matchAll(/<li class="feed-item">([\s\S]*?)<\/li>/g)].map(match => match[1]);
  const counts = html => rankingEntries(html).map(({ name, count }) => [name, count]);

  await t.test('the top five include activities beyond the first 100 feed items', async () => {
    const home = await page();
    const expected = [['acme/widget', 104], ['acme/widget-tools', 8], ['acme/docs', 4],
      ['elsewhere/runner', 2], ['elsewhere/alpha', 1]];
    assert.deepEqual(counts(home), expected);
    assert.ok(home.includes('Top 5 of 6 repositories'));
    assert.ok(home.includes('showing 100'));
    assert.deepEqual(counts(await page('/?show=all')), expected);
  });

  await t.test('ranking links preserve combined filters and select one exact repository', async () => {
    const home = await page('/?q=widget&owner=acme&view=user&kind=pull&kind=issue&label=bug&show=all');
    assert.deepEqual(counts(home), [['acme/widget-tools', 2], ['acme/widget', 1]]);
    const target = rankingEntries(home).find(entry => entry.name === 'acme/widget');
    const params = target.href.searchParams;
    assert.equal(params.get('repo'), 'acme/widget');
    assert.equal(params.get('q'), 'widget');
    assert.equal(params.get('owner'), 'acme');
    assert.equal(params.get('view'), 'user');
    assert.equal(params.get('show'), 'all');
    assert.deepEqual(params.getAll('kind'), ['pull', 'issue']);
    assert.deepEqual(params.getAll('label'), ['bug']);

    const selected = await page(target.href);
    assert.deepEqual(counts(selected), counts(home));
    const rows = feedRows(selected);
    assert.equal(rows.length, 1);
    assert.ok(rows[0].includes('Widget bug'));
    assert.match(selected, /Repositories\s*<\/div>\s*<div class="stat-value">1<\/div>/);
    assert.ok(selected.includes('name="repo" value="acme/widget"'));
    assert.ok(selected.includes('noindex'));
    const active = rankingEntries(selected).find(entry => entry.active);
    assert.equal(active.name, 'acme/widget');
    assert.equal(active.href.searchParams.has('repo'), false);
    assert.equal(active.href.searchParams.get('q'), 'widget');
    assert.deepEqual(active.href.searchParams.getAll('kind'), ['pull', 'issue']);
    assert.deepEqual(counts(await page(active.href)), counts(home));

    const projectLink = selected.match(/href="([^\"]+)"[^>]*>\s*By project/);
    assert.ok(projectLink);
    assert.equal(new URL(projectLink[1].replaceAll('&amp;', '&'), app.base).searchParams.get('repo'), 'acme/widget');
  });

  await t.test('repository selection ignores case and does not match name prefixes', async () => {
    const home = await page('/?repo=ACME/WIDGET&show=all');
    const rows = feedRows(home);
    assert.equal(rows.length, 104);
    assert.ok(rows.some(row => row.includes('Widget bug')));
    assert.ok(rows.every(row => !row.includes('Tools bug') && !row.includes('Tools feature')));
    assert.equal(rankingEntries(home).find(entry => entry.active).name, 'acme/widget');
  });

  await t.test('single-repository rankings use the full bar width', async () => {
    const home = await page('/?q=elsewhere/runner');
    assert.deepEqual(counts(home), [['elsewhere/runner', 2]]);
    assert.match(home, /width:\s*100%/);
  });

  await t.test('empty filters and unknown repository selections remain recoverable', async () => {
    const noSearch = await page('/?q=missing');
    assert.deepEqual(counts(noSearch), []);
    assert.ok(!noSearch.includes('id="activity-ranking-title"'));
    const noActivity = await page('/?kind=pull&label=bug');
    assert.deepEqual(counts(noActivity), []);
    assert.ok(noActivity.includes('No activity matches these filters'));
    const missingRepository = await page('/?repo=acme/missing');
    assert.equal(feedRows(missingRepository).length, 0);
    assert.ok(missingRepository.includes('Selected repository is outside these filters'));
    assert.ok(missingRepository.includes('Clear repository filter'));
  });

  await t.test('an empty instance omits the chart', async () => {
    fixtures = [];
    const home = await page();
    assert.ok(!home.includes('id="activity-ranking-title"'));
    assert.ok(home.includes('Nothing tracked yet'));
  });
});
