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
