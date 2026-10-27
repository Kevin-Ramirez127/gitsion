import type { OwnerSummary, Repository } from './types';

const baseURL = (
  process.env.BACKEND_URL ||
  (import.meta.env.BACKEND_URL as string | undefined) ||
  'http://localhost:8080'
).replace(/\/+$/, '');

export class ApiError extends Error {
  status: number;
  code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  let response: Response;
  try {
    response = await fetch(baseURL + path, {
      ...init,
      headers: {
        Accept: 'application/json',
        ...(init.body ? { 'Content-Type': 'application/json' } : {}),
        ...init.headers,
      },
      signal: AbortSignal.timeout(30_000),
    });
  } catch {
    throw new ApiError(
      0,
      'network_error',
      'Could not load the feed. Try again shortly.',
    );
  }

  if (response.status === 204) {
    return undefined as T;
  }

  const text = await response.text();
  let payload: unknown = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = null;
    }
  }

  if (!response.ok) {
    const envelope = payload as { error?: { code?: string; message?: string } } | null;
    throw new ApiError(
      response.status,
      envelope?.error?.code ?? 'unknown_error',
      envelope?.error?.message ?? `Request failed with status ${response.status}`,
    );
  }

  return payload as T;
}

export async function listRepositories(owner?: string): Promise<Repository[]> {
  const query = owner ? `?owner=${encodeURIComponent(owner)}` : '';
  const data = await request<{ repositories: Repository[] }>(`/api/v1/repos${query}`);
  return data.repositories ?? [];
}

export async function getRepository(owner: string, name: string): Promise<Repository> {
  return request<Repository>(
    `/api/v1/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`,
  );
}

export async function listOwners(): Promise<OwnerSummary[]> {
  const data = await request<{ owners: OwnerSummary[] }>('/api/v1/owners');
  return data.owners ?? [];
}

export async function trackRepository(repository: string): Promise<Repository> {
  return request<Repository>('/api/v1/repos', {
    method: 'POST',
    headers: adminHeaders(),
    body: JSON.stringify({ repository }),
  });
}

export async function untrackRepository(owner: string, name: string): Promise<void> {
  await request<void>(
    `/api/v1/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`,
    { method: 'DELETE', headers: adminHeaders() },
  );
}

export async function refreshRepository(owner: string, name: string): Promise<Repository> {
  return request<Repository>(
    `/api/v1/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/refresh`,
    { method: 'POST', headers: adminHeaders() },
  );
}

export async function syncAll(): Promise<void> {
  await request<{ status: string }>('/api/v1/sync', {
    method: 'POST',
    headers: adminHeaders(),
  });
}

/** Checks a candidate token against the backend without creating a session. */
export async function verifyAdminSession(token: string): Promise<void> {
  await request<void>('/api/v1/admin/session', {
    headers: { Authorization: `Bearer ${token}` },
  });
}

function adminHeaders(): Record<string, string> {
  const token = (process.env.ADMIN_TOKEN ?? '').trim();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

/** Turns an unknown thrown value into a message safe to show in a flash. */
export function errorMessage(error: unknown, fallback = 'Something went wrong.'): string {
  if (error instanceof ApiError) return error.message;
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}
