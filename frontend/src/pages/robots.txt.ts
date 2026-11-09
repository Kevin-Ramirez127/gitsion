import type { APIRoute } from 'astro';
import { absoluteUrl, isSiteUrlConfigured } from '../lib/seo';

/**
 * Crawlers may index every public page. Admin, API and health endpoints are
 * private or machine-only. The sitemap is absolute so it works behind a proxy
 * once `SITE_URL` is set.
 */
export const GET: APIRoute = ({ url, request }) => {
  const body = [
    'User-agent: *',
    'Allow: /',
    'Disallow: /admin',
    'Disallow: /admin/',
    'Disallow: /api/',
    'Disallow: /healthz',
    '',
    `Sitemap: ${absoluteUrl('/sitemap.xml', url, request.headers)}`,
    '',
  ].join('\n');

  return new Response(body, {
    status: 200,
    headers: {
      'Content-Type': 'text/plain; charset=utf-8',
      'Cache-Control': isSiteUrlConfigured()
        ? 'public, max-age=3600, s-maxage=3600'
        : 'no-store',
    },
  });
};
