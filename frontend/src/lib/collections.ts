import type { Issue, PullRequest, Repository } from './types';

export interface IssueEntry {
  issue: Issue;
  repo: Repository;
}

export interface PullRequestEntry {
  pr: PullRequest;
  repo: Repository;
}

export interface TagCount {
  tag: string;
  count: number;
}

function timeValue(value?: string | null): number {
  if (!value) return 0;
  const time = new Date(value).getTime();
  if (Number.isNaN(time) || new Date(time).getUTCFullYear() < 1970) return 0;
  return time;
}

/** Legacy snapshots only contain open issues, so an empty state means open. */
export function isOpenIssue(issue: Issue): boolean {
  return issue.state !== 'closed';
}

function issueTime(entry: IssueEntry, closed: boolean): number {
  const { issue } = entry;
  if (closed) {
    return timeValue(issue.closedAt) || timeValue(issue.updatedAt) || timeValue(issue.createdAt);
  }
  return timeValue(issue.updatedAt) || timeValue(issue.createdAt);
}

function pullRequestTime(entry: PullRequestEntry, closed: boolean): number {
  const { pr } = entry;
  if (closed) {
    return (
      timeValue(pr.closedAt) ||
      timeValue(pr.mergedAt) ||
      timeValue(pr.updatedAt) ||
      timeValue(pr.createdAt)
    );
  }
  return timeValue(pr.updatedAt) || timeValue(pr.createdAt);
}

function compareEntries(
  a: { repo: Repository; issue?: Issue; pr?: PullRequest },
  b: { repo: Repository; issue?: Issue; pr?: PullRequest },
): number {
  const aNumber = a.issue?.number ?? a.pr?.number ?? 0;
  const bNumber = b.issue?.number ?? b.pr?.number ?? 0;
  return a.repo.fullName.localeCompare(b.repo.fullName) || aNumber - bNumber;
}

export function sortIssueEntries(entries: IssueEntry[], closed: boolean): IssueEntry[] {
  return [...entries].sort((a, b) => {
    return issueTime(b, closed) - issueTime(a, closed) || compareEntries(a, b);
  });
}

export function sortPullRequestEntries(
  entries: PullRequestEntry[],
  closed: boolean,
): PullRequestEntry[] {
  return [...entries].sort((a, b) => {
    return pullRequestTime(b, closed) - pullRequestTime(a, closed) || compareEntries(a, b);
  });
}

export function collectIssues(repos: Repository[]): IssueEntry[] {
  return repos.flatMap((repo) => repo.issues.map((issue) => ({ issue, repo })));
}

export function collectPullRequests(repos: Repository[]): PullRequestEntry[] {
  return repos.flatMap((repo) => {
    const prs =
      repo.pullRequests && repo.pullRequests.length > 0
        ? repo.pullRequests
        : repo.latestPr
          ? [repo.latestPr]
          : [];
    return prs.map((pr) => ({ pr, repo }));
  });
}

/** Counts issue tags across every issue, ordered by frequency then name. */
export function countIssueTags(entries: IssueEntry[]): TagCount[] {
  const counts = new Map<string, number>();
  for (const { issue } of entries) {
    for (const label of new Set(issue.labels)) {
      counts.set(label, (counts.get(label) ?? 0) + 1);
    }
  }
  return [...counts.entries()]
    .map(([tag, count]) => ({ tag, count }))
    .sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag));
}

export function filterIssuesByTags(entries: IssueEntry[], tags: Set<string>): IssueEntry[] {
  if (tags.size === 0) return entries;
  return entries.filter(({ issue }) => issue.labels.some((label) => tags.has(label)));
}
