import assert from 'node:assert/strict';
import test from 'node:test';
import {
  activityActionLabel,
  buildActivityFeed,
  groupActivities,
} from '../src/lib/activity.ts';

const owner = {
  login: 'gauchitodev',
  avatarUrl: 'https://avatars.example/gauchitodev',
  htmlUrl: 'https://github.com/gauchitodev',
};
const contributor = {
  login: 'carlosplanchon',
  avatarUrl: 'https://avatars.example/carlosplanchon',
  htmlUrl: 'https://github.com/carlosplanchon',
};
const before = '1'.repeat(40);
const after = '2'.repeat(40);

function push(overrides = {}) {
  return {
    id: 101,
    actor: contributor,
    ref: 'refs/heads/feature/attribution',
    before,
    after,
    timestamp: '2026-02-01T10:00:00Z',
    forced: false,
    ...overrides,
  };
}

function repo(overrides = {}) {
  return {
    fullName: 'gauchitodev/ClaudIA',
    name: 'ClaudIA',
    owner,
    htmlUrl: 'https://github.com/gauchitodev/ClaudIA',
    defaultBranch: 'main',
    pushedAt: '2026-02-04T10:00:00Z',
    latestPr: null,
    issues: [],
    ...overrides,
  };
}

test('pushes retain each actual actor, branch, timestamp and stable identity', () => {
  const activities = buildActivityFeed([repo({
    pushes: [
      push(),
      push({
        id: 102,
        actor: owner,
        ref: 'refs/heads/main',
        timestamp: '2026-02-02T10:00:00Z',
      }),
    ],
  })]);
  assert.equal(activities.length, 2);
  assert.equal(activities[0].actor.login, 'gauchitodev');
  assert.equal(activities[1].actor.login, 'carlosplanchon');
  assert.equal(activities[1].actor.avatarUrl, contributor.avatarUrl);
  assert.equal(activities[1].branch, 'feature/attribution');
  assert.equal(activities[1].timestamp, '2026-02-01T10:00:00.000Z');
  assert.equal(activities[1].url, `https://github.com/gauchitodev/ClaudIA/compare/${before}...${after}`);
  assert.notEqual(activities[0].id, activities[1].id);
  assert.deepEqual(
    buildActivityFeed([repo({ pushes: [push()] })]).map(a => a.id),
    [activities[1].id],
  );
});

test('person view groups pushes under the actual pusher', () => {
  const activities = buildActivityFeed([repo({
    pushes: [push(), push({ id: 102, actor: owner })],
  })]);
  const groups = groupActivities(activities, 'user');
  assert.deepEqual(new Set(groups.map(group => group.label)),
    new Set(['carlosplanchon', 'gauchitodev']));
  const carlos = groups.find(group => group.label === 'carlosplanchon');
  assert.equal(carlos.avatarUrl, contributor.avatarUrl);
  assert.equal(carlos.activities.length, 1);
});

test('legacy snapshots never invent a push using the repository owner', () => {
  assert.deepEqual(buildActivityFeed([repo()]), []);
  assert.deepEqual(buildActivityFeed([repo({ pushes: null })]), []);
});

test('unattributed or incomplete pushes never fall back to the owner', () => {
  const activities = buildActivityFeed([repo({
    pushes: [
      push({ actor: null }),
      push({ actor: { ...contributor, login: '' } }),
      push({ timestamp: 'invalid' }),
      push({ ref: '' }),
      push({ after: '0'.repeat(40) }),
    ],
  })]);
  assert.deepEqual(activities, []);
});

test('force pushes are labelled and initial pushes link to the exact commit', () => {
  const [activity] = buildActivityFeed([repo({
    pushes: [push({ before: '0'.repeat(40), forced: true })],
  })]);
  assert.equal(activityActionLabel(activity), 'force-pushed to');
  assert.equal(activity.url, `https://github.com/gauchitodev/ClaudIA/commit/${after}`);
  assert.equal(activityActionLabel({ ...activity, forced: false }), 'pushed to');
});

test('push history coexists with issues and pull requests in chronological order', () => {
  const activities = buildActivityFeed([repo({
    pushes: [push()],
    latestPr: {
      number: 1, title: 'PR', state: 'open', author: contributor, htmlUrl: '/pull/1',
      createdAt: '2026-02-03T10:00:00Z', updatedAt: '2026-02-03T10:00:00Z',
    },
    issues: [{
      number: 2, title: 'Issue', state: 'open', author: owner, htmlUrl: '/issues/2',
      createdAt: '2026-02-02T10:00:00Z', updatedAt: '2026-02-02T10:00:00Z',
    }],
  })]);
  assert.deepEqual(activities.map(activity => activity.kind), ['pull', 'issue', 'push']);
});

function pull(overrides = {}) {
  return {
    number: 1, title: 'PR', state: 'open', draft: false,
    author: contributor, htmlUrl: '/pull/1',
    createdAt: '2026-02-01T10:00:00Z', updatedAt: '2026-02-03T10:00:00Z',
    ...overrides,
  };
}

test('a merged PR is attributed to the merger, independently of its author', () => {
  const [activity] = buildActivityFeed([repo({ latestPr: pull({
    state: 'merged', mergedBy: owner, mergedAt: '2026-02-02T10:00:00Z',
  }) })]);
  assert.equal(activity.actor.login, owner.login);
  assert.equal(activity.timestamp, '2026-02-02T10:00:00.000Z');
  assert.equal(activityActionLabel(activity), 'merged pull request');
  assert.equal(groupActivities([activity], 'user')[0].label, owner.login);
});

test('unknown merge, close and update actors never fall back to the author', () => {
  for (const state of ['merged', 'closed', 'open']) {
    const [activity] = buildActivityFeed([repo({ latestPr: pull({
      state, mergedAt: state === 'merged' ? '2026-02-02T10:00:00Z' : null,
      closedAt: state === 'closed' ? '2026-02-02T10:00:00Z' : null,
    }) })]);
    const action = state === 'open' ? 'updated' : state;
    assert.equal(activity.actor, undefined);
    assert.equal(activityActionLabel(activity), `Pull request ${action}`);
    const [group] = groupActivities([activity], 'user');
    assert.equal(group.label, 'Other activity');
    assert.equal(group.href, undefined);
    assert.equal(group.avatarUrl, undefined);
    if (state === 'closed') assert.equal(activity.timestamp, '2026-02-02T10:00:00.000Z');
  }
});

test('issue updates remain visible without guessing who made the update', () => {
  const [activity] = buildActivityFeed([repo({ issues: [{
    number: 2, title: 'Issue', state: 'open', author: contributor,
    htmlUrl: '/issues/2', labels: ['bug'],
    createdAt: '2026-02-01T10:00:00Z', updatedAt: '2026-02-03T10:00:00Z',
  }] })]);
  assert.equal(activity.actor, undefined);
  assert.equal(activityActionLabel(activity), 'Issue updated');
  assert.deepEqual(activity.labels, ['bug']);
});

test('new issues and PRs use the known creation actor and creation time', () => {
  const created = '2026-02-01T10:00:00Z';
  const updated = '2026-02-01T10:00:02Z';
  const activities = buildActivityFeed([repo({
    latestPr: pull({ createdAt: created, updatedAt: updated }),
    issues: [{ number: 2, title: 'Issue', author: owner, htmlUrl: '/issues/2',
      createdAt: created, updatedAt: updated }],
  })]);
  for (const activity of activities) {
    assert.equal(activity.action, 'opened');
    assert.equal(activity.timestamp, '2026-02-01T10:00:00.000Z');
    assert.ok(activity.actor);
  }
});

test('the feed includes older cached PRs even when latestPr is merged', () => {
  const latest = pull({ number: 2, state: 'merged', mergedAt: '2026-02-03T10:00:00Z' });
  const older = pull({ number: 1, updatedAt: '2026-02-02T10:00:00Z' });
  const activities = buildActivityFeed([repo({ latestPr: latest, pullRequests: [latest, older] })]);
  assert.deepEqual(activities.map(a => a.number), [2, 1]);
  assert.equal(new Set(activities.map(a => a.id)).size, 2);
});
