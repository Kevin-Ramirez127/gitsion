import type { APIRoute } from 'astro';
import { listRepositories } from '../lib/backend';
import { repositoryPath } from '../lib/format';
import { absoluteUrl, isSiteUrlConfigured } from '../lib/seo';
import type { Repository } from '../lib/types';

interface SitemapEntry {
  route: string;
  lastmod?: string;
  changefreq: string;
  priority: string;
}

const CACHE_SECONDS = 300;

function latest(values: Array<string | null | undefined>): string | undefined {
  let newest: number | undefined;
  for (const value of values) {
    if (!value) continue;
    const time = Date.parse(value);
    if (Number.isNaN(time)) continue;
    if (newest === undefined || time > newest) newest = time;
  }
  return newest === undefined ? undefined : new Date(newest).toISOString();
}

function pullRequests(repo: Repository) {
  if (repo.pullRequests?.length) return repo.pullRequests;
  return repo.latestPr ? [repo.latestPr] : [];
}

function xmlEscape(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

/**
 * Lists every indexable document: the dashboard, the two collection pages and
 * one page per tracked repository. Filtered and paginated query variants are
 * deliberately excluded because they canonicalize to these clean URLs.
 */
export const GET: APIRoute = async ({ url, request }) => {
  let repos: Repository[] = [];
  try {
    repos = await listRepositories();
  } catch {
    // The static pages are still useful when the backend is unavailable.
  }

  const homeUpdated = latest(
    repos.flatMap((repo) => [
      repo.pushedAt,
      ...repo.issues.map((issue) => issue.updatedAt),
      ...pullRequests(repo).map((pr) => pr.updatedAt),
    ]),
  );
  const issuesUpdated = latest(
    repos.flatMap((repo) => repo.issues.map((issue) => issue.updatedAt)),
  );
  const pullsUpdated = latest(
    repos.flatMap((repo) => pullRequests(repo).map((pr) => pr.updatedAt)),
  );

  const entries: SitemapEntry[] = [
    { route: '/', lastmod: homeUpdated, changefreq: 'hourly', priority: '1.0' },
    { route: '/issues', lastmod: issuesUpdated, changefreq: 'daily', priority: '0.7' },
    { route: '/pulls', lastmod: pullsUpdated, changefreq: 'daily', priority: '0.7' },
  ];

  for (const repo of repos) {
    entries.push({
      route: repositoryPath(repo.owner.login, repo.name),
      lastmod: latest([
        repo.pushedAt,
        ...repo.issues.map((issue) => issue.updatedAt),
        ...pullRequests(repo).map((pr) => pr.updatedAt),
      ]),
      changefreq: 'daily',
      priority: '0.8',
    });
  }

  const urls = entries
    .map((entry) => {
      const lines = [
        '  <url>',
        `    <loc>${xmlEscape(absoluteUrl(entry.route, url, request.headers))}</loc>`,
      ];
      if (entry.lastmod) {
        lines.push(`    <lastmod>${xmlEscape(entry.lastmod)}</lastmod>`);
      }
      lines.push(`    <changefreq>${entry.changefreq}</changefreq>`);
      lines.push(`    <priority>${entry.priority}</priority>`);
      lines.push('  </url>');
      return lines.join('\n');
    })
    .join('\n');

  const body = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    urls,
    '</urlset>',
    '',
  ].join('\n');

  return new Response(body, {
    status: 200,
    headers: {
      'Content-Type': 'application/xml; charset=utf-8',
      'Cache-Control': isSiteUrlConfigured()
        ? `public, max-age=${CACHE_SECONDS}, s-maxage=${CACHE_SECONDS}`
        : 'no-store',
    },
  });
};
