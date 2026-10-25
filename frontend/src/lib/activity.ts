import type { Issue, PullRequest, Repository, User } from './types';

/** How many activities the dashboard shows before offering the full feed. */
export const FEED_PAGE_SIZE = 100;

export type ActivityKind = 'push' | 'pull' | 'issue';
export type ActivityAction = 'pushed' | 'opened' | 'updated' | 'merged' | 'closed';

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
  /** UTC calendar day, `YYYY-MM-DD`. */
  key: string;
  label: string;
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

const dayFormatter = new Intl.DateTimeFormat('en', {
  weekday: 'short',
  month: 'short',
  day: 'numeric',
  year: 'numeric',
  timeZone: 'UTC',
});

function dayKey(value: Date): string {
  return value.toISOString().slice(0, 10);
}

/** Groups a sorted activity list into UTC calendar days, newest first. */
export function groupActivitiesByDay(
  activities: Activity[],
  now: Date = new Date(),
): ActivityGroup[] {
  const today = dayKey(now);
  const yesterdayDate = new Date(now);
  yesterdayDate.setUTCDate(yesterdayDate.getUTCDate() - 1);
  const yesterday = dayKey(yesterdayDate);

  const groups = new Map<string, Activity[]>();
  for (const activity of activities) {
    const key = activity.timestamp.slice(0, 10);
    const list = groups.get(key);
    if (list) {
      list.push(activity);
    } else {
      groups.set(key, [activity]);
    }
  }

  return [...groups].map(([key, items]) => ({
    key,
    label:
      key === today
        ? 'Today'
        : key === yesterday
          ? 'Yesterday'
          : dayFormatter.format(new Date(`${key}T00:00:00Z`)),
    activities: items,
  }));
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
