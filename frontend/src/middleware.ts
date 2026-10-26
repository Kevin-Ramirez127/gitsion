import { defineMiddleware } from 'astro:middleware';

/**
 * The dashboard is public; these headers are about safety, not access control.
 * The app only renders server-side HTML and talks to its own API routes, so a
 * restrictive CSP is cheap to enforce.
 */
export const onRequest = defineMiddleware(async (_context, next) => {
  const response = await next();

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

  if (response.headers.get('content-type')?.includes('text/html')) {
    response.headers.set('Cache-Control', 'no-store');
  }

  return response;
});
