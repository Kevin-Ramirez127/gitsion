import type { APIRoute } from 'astro';
import {
  ADMIN_COOKIE,
  clearLoginFailures,
  createAdminSession,
  isSecureRequest,
  loginRateLimit,
  recordLoginFailure,
  verifyAdminToken,
  writesEnabled,
} from '../../../lib/auth';
import { ApiError, errorMessage, verifyAdminSession } from '../../../lib/backend';

export const POST: APIRoute = async (context) => {
  const { request, cookies, redirect, clientAddress } = context;

  let token = '';
  try {
    const form = await request.formData();
    const value = form.get('token');
    if (typeof value === 'string') token = value.trim();
  } catch {
    // Reported as an empty token below.
  }

  if (!writesEnabled()) {
    return redirect(
      '/admin?error=' +
        encodeURIComponent('This dashboard is read-only. Set ADMIN_TOKEN to enable writes.'),
      303,
    );
  }

  if (!token) {
    return redirect('/admin?error=' + encodeURIComponent('Enter the admin token.'), 303);
  }

  const clientKey = clientAddress || 'unknown';
  const limit = loginRateLimit(clientKey);
  if (!limit.allowed) {
    return redirect(
      '/admin?error=' +
        encodeURIComponent(`Too many failed attempts. Try again in ${limit.retryAfter}s.`),
      303,
    );
  }

  if (!verifyAdminToken(token)) {
    recordLoginFailure(clientKey);
    return redirect('/admin?error=' + encodeURIComponent('Invalid admin token.'), 303);
  }

  try {
    await verifyAdminSession(token);
  } catch (error) {
    recordLoginFailure(clientKey);
    const message =
      error instanceof ApiError && error.code === 'unauthorized'
        ? 'The backend rejected the admin token. Check that the frontend and backend use the same ADMIN_TOKEN.'
        : errorMessage(error, 'Could not verify the admin token with the backend.');
    return redirect('/admin?error=' + encodeURIComponent(message), 303);
  }

  clearLoginFailures(clientKey);
  const session = createAdminSession();
  cookies.set(ADMIN_COOKIE, session.value, {
    path: '/',
    httpOnly: true,
    sameSite: 'strict',
    secure: isSecureRequest(context.url, request.headers),
    maxAge: session.maxAge,
  });

  return redirect(
    '/admin?ok=' + encodeURIComponent('Signed in as administrator.'),
    303,
  );
};
