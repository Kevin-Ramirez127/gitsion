import { createHash, createHmac, timingSafeEqual } from 'node:crypto';

export const ADMIN_COOKIE = 'gitsion_admin';
export const SESSION_TTL_SECONDS = 12 * 60 * 60;

const LOGIN_WINDOW_MS = 15 * 60 * 1000;
const LOGIN_MAX_ATTEMPTS = 5;

/** The server-only admin token. An empty value means read-only mode. */
export function adminToken(): string {
  return (process.env.ADMIN_TOKEN ?? '').trim();
}

export function writesEnabled(): boolean {
  return adminToken() !== '';
}

function sessionKey(): string {
  const secret = (process.env.SESSION_SECRET ?? '').trim();
  return secret || adminToken();
}

function sign(value: string): string {
  return createHmac('sha256', sessionKey()).update(value).digest('base64url');
}

/** Binds a session to the current admin token, so token rotation signs everyone out. */
function tokenFingerprint(): string {
  return createHash('sha256').update(adminToken()).digest('base64url').slice(0, 16);
}

function safeEqual(a: string, b: string): boolean {
  const aHash = createHash('sha256').update(a).digest();
  const bHash = createHash('sha256').update(b).digest();
  return timingSafeEqual(aHash, bHash);
}

/** Compares a submitted token with ADMIN_TOKEN in constant time. */
export function verifyAdminToken(candidate: string): boolean {
  const expected = adminToken();
  if (expected === '' || candidate === '') return false;
  return safeEqual(candidate, expected);
}

export interface AdminSession {
  value: string;
  maxAge: number;
}

/** Creates a stateless, signed session cookie value. */
export function createAdminSession(now = Date.now()): AdminSession {
  const expires = Math.floor(now / 1000) + SESSION_TTL_SECONDS;
  const payload = `${expires}.${tokenFingerprint()}`;
  return {
    value: `${payload}.${sign(payload)}`,
    maxAge: SESSION_TTL_SECONDS,
  };
}

/** Verifies the signature, token fingerprint and expiry of a session cookie. */
export function verifyAdminSessionCookie(value: string | undefined, now = Date.now()): boolean {
  if (!value || adminToken() === '' || sessionKey() === '') return false;

  const parts = value.split('.');
  if (parts.length !== 3) return false;

  const [expiresRaw, fingerprint, signature] = parts;
  if (!/^\d+$/.test(expiresRaw) || fingerprint === '' || signature === '') return false;

  const payload = `${expiresRaw}.${fingerprint}`;
  if (!safeEqual(signature, sign(payload))) return false;
  if (!safeEqual(fingerprint, tokenFingerprint())) return false;

  const expires = Number(expiresRaw);
  return Number.isFinite(expires) && expires > Math.floor(now / 1000);
}

/** True when the browser connection is HTTPS, including behind a TLS proxy. */
export function isSecureRequest(url: URL, headers: Headers): boolean {
  if (url.protocol === 'https:') return true;
  const forwarded = headers.get('x-forwarded-proto');
  return forwarded?.split(',')[0]?.trim().toLowerCase() === 'https';
}

const loginFailures = new Map<string, { count: number; resetAt: number }>();

/** Best-effort, per-process login throttling. */
export function loginRateLimit(key: string, now = Date.now()): { allowed: boolean; retryAfter: number } {
  pruneLoginFailures(now);
  const entry = loginFailures.get(key);
  if (!entry || entry.resetAt <= now) {
    loginFailures.delete(key);
    return { allowed: true, retryAfter: 0 };
  }
  if (entry.count < LOGIN_MAX_ATTEMPTS) {
    return { allowed: true, retryAfter: 0 };
  }
  return {
    allowed: false,
    retryAfter: Math.max(1, Math.ceil((entry.resetAt - now) / 1000)),
  };
}

export function recordLoginFailure(key: string, now = Date.now()): void {
  const entry = loginFailures.get(key);
  if (!entry || entry.resetAt <= now) {
    loginFailures.set(key, { count: 1, resetAt: now + LOGIN_WINDOW_MS });
    return;
  }
  entry.count += 1;
}

export function clearLoginFailures(key: string): void {
  loginFailures.delete(key);
}

function pruneLoginFailures(now: number): void {
  if (loginFailures.size < 10_000) return;
  for (const [key, entry] of loginFailures) {
    if (entry.resetAt <= now) loginFailures.delete(key);
  }
}
