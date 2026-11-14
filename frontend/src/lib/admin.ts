import type { APIRoute } from 'astro';
import { ADMIN_COOKIE, verifyAdminSessionCookie, writesEnabled } from './auth.ts';

/** Authorize the matched handler, including every URL alias Astro accepts. */
export function requireAdminMutation(handler: APIRoute): APIRoute {
  return (context) => {
    // Fetch Metadata survives proxy host rewrites. SameSite=Strict protects
    // the session cookie in browsers that do not send this header.
    const fetchSite = context.request.headers.get('sec-fetch-site')?.trim().toLowerCase();
    if (fetchSite === 'cross-site' || fetchSite === 'same-site') {
      return jsonError(403, 'forbidden', 'cross-origin request rejected');
    }

    const accept = context.request.headers.get('accept') ?? '';
    const wantsJSON = accept.includes('application/json') && !accept.includes('text/html');
    if (!writesEnabled()) {
      return wantsJSON
        ? jsonError(403, 'writes_disabled', 'this dashboard is read-only')
        : context.redirect('/admin?error=' + encodeURIComponent('This dashboard is read-only.'), 303);
    }

    if (!verifyAdminSessionCookie(context.cookies.get(ADMIN_COOKIE)?.value)) {
      return wantsJSON
        ? jsonError(401, 'unauthorized', 'admin sign-in required')
        : context.redirect('/admin?error=' + encodeURIComponent('Admin sign-in required.'), 303);
    }

    return handler(context);
  };
}

function jsonError(status: number, code: string, message: string): Response {
  return new Response(JSON.stringify({ error: { code, message } }), {
    status,
    headers: { 'Content-Type': 'application/json; charset=utf-8' },
  });
}
