import { defineMiddleware } from 'astro:middleware';
import {
  ADMIN_COOKIE,
  verifyAdminSessionCookie,
  writesEnabled,
} from './lib/auth';

const adminRoutes = new Set(['/api/track', '/api/untrack', '/api/refresh', '/api/sync']);

/**
 * Reads stay public. Mutations require a signed admin session, and the backend
 * checks the admin token again. Without ADMIN_TOKEN the whole app stays
 * readable but every mutation is refused.
 */
export const onRequest = defineMiddleware(async (context, next) => {
  context.locals.isAdmin = verifyAdminSessionCookie(
    context.cookies.get(ADMIN_COOKIE)?.value,
  );
  context.locals.writesEnabled = writesEnabled();

  const path = context.url.pathname;
  const method = context.request.method.toUpperCase();

  // Login and logout carry no repository state, so they are not origin-gated.
  // Login still requires the admin token, and the session cookie is SameSite.
  if (
    adminRoutes.has(path) &&
    method === 'POST' &&
    isCrossSiteRequest(context.request)
  ) {
    console.warn('gitsion: rejected cross-site request', {
      path,
      origin: context.request.headers.get('origin'),
      host: context.request.headers.get('host'),
      forwardedHost: context.request.headers.get('x-forwarded-host'),
      fetchSite: context.request.headers.get('sec-fetch-site'),
    });
    return harden(
      jsonError(403, 'forbidden', 'cross-origin request rejected'),
      path,
    );
  }

  if (adminRoutes.has(path) && method === 'POST' && !context.locals.isAdmin) {
    if (!context.locals.writesEnabled) {
      if (wantsJSON(context.request)) {
        return harden(
          jsonError(403, 'writes_disabled', 'this dashboard is read-only'),
          path,
        );
      }
      return harden(
        context.redirect(
          '/admin?error=' + encodeURIComponent('This dashboard is read-only.'),
          303,
        ),
        path,
      );
    }
    if (wantsJSON(context.request)) {
      return harden(
        jsonError(401, 'unauthorized', 'admin sign-in required'),
        path,
      );
    }
    return harden(
      context.redirect('/admin?error=' + encodeURIComponent('Admin sign-in required.'), 303),
      path,
    );
  }

  return harden(await next(), path);
});

/**
 * Fetch Metadata is authoritative and immune to reverse-proxy host rewrites.
 * When a browser does not send it, the SameSite=Strict admin cookie is the
 * primary CSRF defense; an Origin/host mismatch is inconclusive there because
 * proxies commonly rewrite Host and X-Forwarded-Host.
 */
function isCrossSiteRequest(request: Request): boolean {
  const fetchSite = request.headers.get('sec-fetch-site')?.trim().toLowerCase();
  return fetchSite === 'cross-site' || fetchSite === 'same-site';
}

function wantsJSON(request: Request): boolean {
  const accept = request.headers.get('accept') ?? '';
  return accept.includes('application/json') && !accept.includes('text/html');
}

function jsonError(status: number, code: string, message: string): Response {
  return new Response(JSON.stringify({ error: { code, message } }), {
    status,
    headers: { 'Content-Type': 'application/json; charset=utf-8' },
  });
}

function harden(response: Response, path: string): Response {
  response.headers.set('X-Content-Type-Options', 'nosniff');
  response.headers.set('X-Frame-Options', 'DENY');
  response.headers.set('Referrer-Policy', 'no-referrer');
  response.headers.set('Permissions-Policy', 'camera=(), microphone=(), geolocation=()');
  response.headers.set(
    'Content-Security-Policy',
    [
      "default-src 'self'",
      "img-src 'self' data: https://github.com https://avatars.githubusercontent.com https://*.githubusercontent.com",
      "style-src 'self' 'unsafe-inline'",
      "script-src 'self' 'unsafe-inline'",
      "connect-src 'self' ws: wss:",
      "font-src 'self'",
      "base-uri 'self'",
      "form-action 'self'",
      "frame-ancestors 'none'",
    ].join('; '),
  );

  if (path === '/admin' || path.startsWith('/admin/')) {
    response.headers.set('X-Robots-Tag', 'noindex, nofollow');
  }

  if (response.headers.get('content-type')?.includes('text/html')) {
    response.headers.set('Cache-Control', 'no-store');
  }

  return response;
}
