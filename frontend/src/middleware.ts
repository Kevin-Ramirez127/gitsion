import { defineMiddleware } from 'astro:middleware';
import {
  ADMIN_COOKIE,
  verifyAdminSessionCookie,
  writesEnabled,
} from './lib/auth';

/**
 * Expose session state to pages and harden every response. Administrative
 * handlers enforce authorization themselves, independently of the URL spelling.
 */
export const onRequest = defineMiddleware(async (context, next) => {
  context.locals.isAdmin = verifyAdminSessionCookie(
    context.cookies.get(ADMIN_COOKIE)?.value,
  );
  context.locals.writesEnabled = writesEnabled();

  const path = context.url.pathname;
  return harden(await next(), path);
});

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
