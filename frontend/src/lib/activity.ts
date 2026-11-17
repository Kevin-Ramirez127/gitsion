import type { Issue, PullRequest, Push, Repository, User } from './types';
import { repositoryPath } from './format.ts';
import { repositoryPullRequests } from './collections.ts';

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
  /** Only set when the actor of this action is known, not its original author. */
  actor?: User;
  title: string;
  /** GitHub URL for the underlying push, pull request or issue. */
  url: string;
  repo: Repository;
  number?: number;
  branch?: string;
  labels?: string[];
  draft?: boolean;
  forced?: boolean;
}

export interface ActivityGroup {
  key: string;
  label: string;
  href?: string;
  avatarUrl?: string;
  latest: string;
  activities: Activity[];
}

export interface RepositoryActivityCount {
  fullName: string;
  count: number;
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
  let time = updated || created;
  if (pr.state === 'merged') time = merged || timestamp(pr.closedAt) || updated;
  else if (pr.state === 'closed') time = timestamp(pr.closedAt) || updated;
  if (!time) return null;

  let action: ActivityAction;
  if (pr.state === 'merged') {
    action = 'merged';
  } else if (pr.state === 'closed') {
    action = 'closed';
  } else if (created && time >= created && time - created <= OPENED_WINDOW_MS) {
    action = 'opened';
    time = created;
  } else {
    action = 'updated';
  }

  let actor: User | undefined;
  if (action === 'merged' && pr.mergedBy?.login) actor = pr.mergedBy;
  else if (action === 'opened') actor = pr.author;

  return {
    id: `pull:${repo.fullName}#${pr.number}`,
    kind: 'pull',
    action,
    timestamp: new Date(time).toISOString(),
    actor,
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
  let time = updated || created;
  if (!time) return null;

  const action: ActivityAction =
    created && time >= created && time - created <= OPENED_WINDOW_MS ? 'opened' : 'updated';
  if (action === 'opened') time = created;

  return {
    id: `issue:${repo.fullName}#${issue.number}`,
    kind: 'issue',
    action,
    timestamp: new Date(time).toISOString(),
    actor: action === 'opened' ? issue.author : undefined,
    title: issue.title,
    url: issue.htmlUrl,
    repo,
    number: issue.number,
    labels: issue.labels,
  };
}

function pushActivity(repo: Repository, push: Push): Activity | null {
  const time = timestamp(push.timestamp);
  if (
    !time || !push.actor?.login || push.id <= 0 || !push.ref ||
    !push.after || /^0+$/.test(push.after)
  ) return null;

  const branch = push.ref.replace(/^refs\/heads\//, '');
  const after = encodeURIComponent(push.after);
  const hasBefore = push.before && !/^0+$/.test(push.before);
  return {
    id: `push:${repo.fullName}:${push.id}`,
    kind: 'push',
    action: 'pushed',
    timestamp: new Date(time).toISOString(),
    actor: push.actor,
    title: branch,
    url: hasBefore
      ? `${repo.htmlUrl}/compare/${encodeURIComponent(push.before)}...${after}`
      : `${repo.htmlUrl}/commit/${after}`,
    repo,
    branch,
    forced: push.forced,
  };
}

/** Turns cached repository snapshots into a newest-first activity stream. */
export function buildActivityFeed(repos: Repository[]): Activity[] {
  const activities: Activity[] = [];

  for (const repo of repos) {
    // A legacy pushedAt timestamp has no actor or ref; do not invent a push.
    for (const push of repo.pushes ?? []) {
      const activity = pushActivity(repo, push);
      if (activity) activities.push(activity);
    }

    for (const pr of repositoryPullRequests(repo)) {
      const pull = pullActivity(repo, pr);
      if (pull) activities.push(pull);
    }

    for (const issue of repo.issues) {
      if (issue.state === 'closed') continue;
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

export type ActivityWindow = '7d' | '30d' | 'all';

export function parseActivityWindow(value: string | null): ActivityWindow {
  return value === '30d' || value === 'all' ? value : '7d';
}

/** A rolling UTC duration over captured items, not a complete event history. */
export function filterActivityWindow(
  activities: Activity[],
  window: ActivityWindow,
  now = Date.now(),
): Activity[] {
  if (window === 'all') return activities;
  const cutoff = now - (window === '7d' ? 7 : 30) * 24 * 60 * 60 * 1000;
  return activities.filter((activity) => {
    const time = Date.parse(activity.timestamp);
    return Number.isFinite(time) && time >= cutoff && time <= now;
  });
}

/** Counts the complete filtered feed, before its display limit is applied. */
export function rankRepositoryActivity(activities: Activity[]): RepositoryActivityCount[] {
  const counts = new Map<string, number>();
  for (const activity of activities) {
    const name = activity.repo.fullName;
    counts.set(name, (counts.get(name) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([fullName, count]) => ({ fullName, count }))
    .sort((a, b) => b.count - a.count || a.fullName.localeCompare(b.fullName));
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
        : activity.actor ? `user:${activity.actor.login.toLowerCase()}` : 'unattributed';

    let group = groups.get(key);
    if (!group) {
      if (view === 'project') {
        group = {
          key,
          label: activity.repo.fullName,
          href: repositoryPath(activity.repo.owner.login, activity.repo.name),
          avatarUrl: activity.repo.owner.avatarUrl,
          latest: activity.timestamp,
          activities: [],
        };
      } else if (activity.actor) {
        group = {
          key,
          label: activity.actor.login,
          href: activity.actor.htmlUrl,
          avatarUrl: activity.actor.avatarUrl,
          latest: activity.timestamp,
          activities: [],
        };
      } else {
        group = {
          key,
          label: 'Other activity',
          latest: activity.timestamp,
          activities: [],
        };
      }
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
      return activity.forced ? 'force-pushed to' : 'pushed to';
    case 'pull':
      return activity.actor ? `${activity.action} pull request` : `Pull request ${activity.action}`;
    case 'issue':
      return activity.actor ? `${activity.action} issue` : `Issue ${activity.action}`;
  }
}
