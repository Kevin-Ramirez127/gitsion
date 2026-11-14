import assert from 'node:assert/strict';
import test from 'node:test';
import {
  collectPullRequests,
  countIssueTags,
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

test('issue tag filters narrow the next set of available tags', () => {
  const entry = (labels) => ({ issue: { labels }, repo: { fullName: 'acme/widget' } });
  const entries = [
    entry(['bug', 'ui']),
    entry(['bug']),
    entry(['docs']),
  ];

  assert.deepEqual(countIssueTags(entries).map(({ tag, count }) => [tag, count]), [
    ['bug', 2],
    ['docs', 1],
    ['ui', 1],
  ]);

  const matching = filterIssuesByTags(entries, new Set(['bug']));
  assert.deepEqual(countIssueTags(matching).map(({ tag }) => tag), ['bug', 'ui']);
});
