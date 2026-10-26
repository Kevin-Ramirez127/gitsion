import type { Issue, PullRequest, Repository, User } from './types';
import { repositoryPath } from './format';

/** How many activities the dashboard shows before offering the full feed. */
export const FEED_PAGE_SIZE = 100;

export type ActivityKind = 'push' | 'pull' | 'issue';
export type ActivityAction = 'pushed' | 'opened' | 'updated' | 'merged' | 'closed';
export type FeedView = 'project' | 'user';

export interface Activity {
  /** Stable identifier, unique across repositories. */
  id: string;
  kind: ActivityKind;
  action: ActivityAction;
  timestamp: string;
  actor: User;
  title: string;
  /** GitHub URL for the underlying push, pull request or issue. */
  url: string;
  repo: Repository;
  number?: number;
  branch?: string;
  labels?: string[];
  draft?: boolean;
}

export interface ActivityGroup {
  key: string;
  label: string;
  href: string;
  avatarUrl: string;
  latest: string;
  activities: Activity[];
}

/**
 * Issues and pull requests that were never touched after creation are reported
 * as "opened". GitHub sets `updated_at` a second or two after `created_at`, so
 * a small window keeps fresh items from being labelled "updated".
 */
const OPENED_WINDOW_MS = 60_000;

function timestamp(value?: string | null): number {
  if (!value) return 0;
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return 0;
  if (new Date(time).getUTCFullYear() < 1970) return 0;
  return time;
}

function pullActivity(repo: Repository, pr: PullRequest): Activity | null {
  const updated = timestamp(pr.updatedAt);
  const created = timestamp(pr.createdAt);
  const merged = timestamp(pr.mergedAt);
  const time = pr.state === 'merged' ? merged || updated : updated;
  if (!time) return null;

  let action: ActivityAction;
  if (pr.state === 'merged') {
    action = 'merged';
  } else if (pr.state === 'closed') {
    action = 'closed';
  } else if (created && time - created <= OPENED_WINDOW_MS) {
    action = 'opened';
  } else {
    action = 'updated';
  }

  return {
    id: `pull:${repo.fullName}#${pr.number}`,
    kind: 'pull',
    action,
    timestamp: new Date(time).toISOString(),
    actor: pr.author,
    title: pr.title,
    url: pr.htmlUrl,
    repo,
    number: pr.number,
    draft: pr.draft,
  };
}

function issueActivity(repo: Repository, issue: Issue): Activity | null {
  const created = timestamp(issue.createdAt);
  const updated = timestamp(issue.updatedAt);
  const time = updated || created;
  if (!time) return null;

  const action: ActivityAction =
    created && time - created <= OPENED_WINDOW_MS ? 'opened' : 'updated';

  return {
    id: `issue:${repo.fullName}#${issue.number}`,
    kind: 'issue',
    action,
    timestamp: new Date(time).toISOString(),
    actor: issue.author,
    title: issue.title,
    url: issue.htmlUrl,
    repo,
    number: issue.number,
    labels: issue.labels,
  };
}

function pushActivity(repo: Repository): Activity | null {
  const time = timestamp(repo.pushedAt);
  if (!time) return null;

  const branch = repo.defaultBranch || undefined;
  return {
    id: `push:${repo.fullName}`,
    kind: 'push',
    action: 'pushed',
    timestamp: new Date(time).toISOString(),
    // Snapshots do not include the pusher, so the repository owner stands in.
    actor: repo.owner,
    title: branch ?? 'default branch',
    url: branch
      ? `${repo.htmlUrl}/commits/${encodeURIComponent(branch)}`
      : `${repo.htmlUrl}/commits`,
    repo,
    branch,
  };
}

/** Turns cached repository snapshots into a newest-first activity stream. */
export function buildActivityFeed(repos: Repository[]): Activity[] {
  const activities: Activity[] = [];

  for (const repo of repos) {
    const push = pushActivity(repo);
    if (push) activities.push(push);

    if (repo.latestPr) {
      const pull = pullActivity(repo, repo.latestPr);
      if (pull) activities.push(pull);
    }

    for (const issue of repo.issues) {
      const item = issueActivity(repo, issue);
      if (item) activities.push(item);
    }
  }

  activities.sort((a, b) => {
    const delta = Date.parse(b.timestamp) - Date.parse(a.timestamp);
    return delta || a.id.localeCompare(b.id);
  });

  return activities;
}

/**
 * Groups a newest-first activity list by project or by actor. Group order
 * follows the newest activity in each group; activities stay chronological
 * inside a group.
 */
export function groupActivities(
  activities: Activity[],
  view: FeedView,
): ActivityGroup[] {
  const groups = new Map<string, ActivityGroup>();

  for (const activity of activities) {
    const key =
      view === 'project'
        ? `project:${activity.repo.fullName}`
        : `user:${activity.actor.login.toLowerCase()}`;

    let group = groups.get(key);
    if (!group) {
      group =
        view === 'project'
          ? {
              key,
              label: activity.repo.fullName,
              href: repositoryPath(activity.repo.owner.login, activity.repo.name),
              avatarUrl: activity.repo.owner.avatarUrl,
              latest: activity.timestamp,
              activities: [],
            }
          : {
              key,
              label: activity.actor.login,
              href: activity.actor.htmlUrl,
              avatarUrl: activity.actor.avatarUrl,
              latest: activity.timestamp,
              activities: [],
            };
      groups.set(key, group);
    }
    group.activities.push(activity);
  }

  return [...groups.values()];
}

/** Human-readable verb phrase shown before an activity title. */
export function activityActionLabel(activity: Activity): string {
  switch (activity.kind) {
    case 'push':
      return 'pushed to';
    case 'pull':
      return `${activity.action} pull request`;
    case 'issue':
      return `${activity.action} issue`;
  }
}
