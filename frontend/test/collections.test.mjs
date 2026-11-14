import assert from 'node:assert/strict';
import test from 'node:test';
import {
  collectPullRequests,
  countIssueStates,
  countIssueTags,
  filterIssuesByState,
  filterIssuesByTags,
  hasOpenPullRequest,
} from '../src/lib/collections.ts';

test('a repository counts once when any cached non-draft PR is open', () => {
  const latestPr = { number: 3, state: 'merged' };
  const repository = { latestPr, pullRequests: [latestPr,
    { number: 2, state: 'open', draft: false },
    { number: 1, state: 'open', draft: false },
  ] };
  assert.equal([repository].filter(hasOpenPullRequest).length, 1);
  assert.deepEqual(collectPullRequests([repository]).map(({ pr }) => pr.number), [3, 2, 1]);
});

test('drafts and closed PRs do not count as active', () => {
  for (const pr of [{ state: 'open', draft: true }, { state: 'closed' }, { state: 'merged' }]) {
    assert.equal(hasOpenPullRequest({ latestPr: pr, pullRequests: [pr] }), false);
  }
  assert.equal(hasOpenPullRequest({ latestPr: null, pullRequests: [] }), false);
});

test('legacy snapshots retain their latestPr fallback', () => {
  const latestPr = { number: 1, state: 'open', draft: false };
  for (const repository of [{ latestPr }, { latestPr, pullRequests: [] }]) {
    assert.equal(hasOpenPullRequest(repository), true);
    assert.equal(collectPullRequests([repository])[0].pr, latestPr);
  }
});

test('issue tag filters require every selected tag and narrow the next set', () => {
  const entry = (labels) => ({ issue: { labels }, repo: { fullName: 'acme/widget' } });
  const entries = [
    entry(['bug', 'ui', 'help wanted']),
    entry(['bug', 'ui']),
    entry(['bug']),
    entry(['docs']),
  ];

  assert.deepEqual(countIssueTags(entries).map(({ tag, count }) => [tag, count]), [
    ['bug', 3],
    ['ui', 2],
    ['docs', 1],
    ['help wanted', 1],
  ]);

  const bugAndUi = filterIssuesByTags(entries, new Set(['bug', 'ui']));
  assert.equal(bugAndUi.length, 2);
  assert.deepEqual(countIssueTags(bugAndUi).map(({ tag }) => tag), [
    'bug',
    'ui',
    'help wanted',
  ]);

  assert.equal(filterIssuesByTags(entries, new Set(['docs', 'ui'])).length, 0);
});

test('status filters are multi-select and legacy issues count as open', () => {
  const entries = [
    { issue: { state: 'open', labels: [] }, repo: {} },
    { issue: { state: 'closed', labels: [] }, repo: {} },
    { issue: { state: undefined, labels: [] }, repo: {} },
  ];

  assert.deepEqual(countIssueStates(entries), { open: 2, closed: 1 });
  assert.equal(filterIssuesByState(entries, new Set()).length, 3);
  assert.deepEqual(
    filterIssuesByState(entries, new Set(['open'])).map(({ issue }) => issue.state),
    ['open', undefined],
  );
  assert.deepEqual(
    filterIssuesByState(entries, new Set(['closed'])).map(({ issue }) => issue.state),
    ['closed'],
  );
  assert.equal(filterIssuesByState(entries, new Set(['open', 'closed'])).length, 3);
});
