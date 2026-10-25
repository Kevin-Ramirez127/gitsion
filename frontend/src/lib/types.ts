export interface User {
  login: string;
  avatarUrl: string;
  htmlUrl: string;
}

export interface Owner {
  login: string;
  type: string;
  avatarUrl: string;
  htmlUrl: string;
}

export interface PullRequest {
  number: number;
  title: string;
  state: 'open' | 'merged' | 'closed' | string;
  draft: boolean;
  author: User;
  htmlUrl: string;
  headRef?: string;
  baseRef?: string;
  createdAt: string;
  updatedAt: string;
  mergedAt?: string | null;
}

export interface Tag {
  name: string;
  commitSha: string;
}

export interface Issue {
  number: number;
  title: string;
  author: User;
  labels: string[];
  comments: number;
  htmlUrl: string;
  createdAt: string;
  updatedAt: string;
}

export interface Repository {
  fullName: string;
  name: string;
  owner: Owner;
  description: string;
  htmlUrl: string;
  language: string;
  defaultBranch: string;
  visibility: string;
  topics: string[];
  stars: number;
  forks: number;
  openItems: number;
  pushedAt: string;
  latestPr: PullRequest | null;
  tags: Tag[];
  issues: Issue[];
  trackedAt: string;
  lastSyncedAt: string;
  lastSyncAttempt: string;
  syncError?: string;
}

export interface OwnerSummary {
  login: string;
  type: string;
  avatarUrl: string;
  htmlUrl: string;
  repoCount: number;
}
