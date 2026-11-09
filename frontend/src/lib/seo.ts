/**
 * Site-wide SEO metadata built with easeo.
 *
 * Every public page calls `buildPageSeo`, which turns the page state into an
 * easeo payload: canonical URL, title, description, robots directives, Open
 * Graph, Twitter Cards and JSON-LD. Filtered or transient views are kept out
 * of the index and point their canonical at the clean page instead, so
 * crawlers only see one URL per document.
 */
import { buildSeoPayload, normalizePublicUrl } from '@easeo/core';
import type {
  Breadcrumb,
  SEOConfig,
  SEOEntity,
  SEOPayload,
} from '@easeo/core';
import { getSiteConfig } from './config';

/** Entity types supported by easeo. */
export type SeoEntityType =
  | 'home'
  | 'page'
  | 'post'
  | 'video'
  | 'taxonomy'
  | 'search'
  | 'other'
  | 'product'
  | 'organization'
  | 'local_business'
  | 'faq';

export interface SeoImage {
  url: string;
  alt?: string;
  width?: number;
  height?: number;
}

export interface SeoInput {
  /** Page title without the site suffix when the caller already added it. */
  title: string;
  /** Meta description. Defaults to a generated fallback. */
  description?: string;
  /** Clean route path used for the canonical URL. Never include a query. */
  route: string;
  /** `false` emits `noindex` and keeps the page out of search results. */
  indexable?: boolean;
  /** `false` emits `nofollow`, used for error and admin pages. */
  followable?: boolean;
  entityType?: SeoEntityType;
  image?: SeoImage;
  breadcrumbs?: Breadcrumb[];
  publishedAt?: string;
  updatedAt?: string;
  authorName?: string;
  /** Upper bound for the resolved title, in characters. */
  maxTitle?: number;
  /** Upper bound for the resolved description, in characters. */
  maxDescription?: number;
}

export interface PageSeo {
  /** Fully resolved easeo payload. */
  payload: SEOPayload;
  /** Complete `<head>` markup, including JSON-LD. */
  head: string;
  canonical: string;
  robots: string;
  title: string;
  description: string;
  noindex: boolean;
  origin: string;
}

const DEFAULT_ORIGIN = 'http://localhost:4321';
const DEFAULT_MAX_TITLE = 60;
const DEFAULT_MAX_DESCRIPTION = 160;

function env(key: string): string | undefined {
  const value = process.env[key];
  if (typeof value !== 'string') return undefined;
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
}

function configuredOrigin(): string | undefined {
  const configured = env('SITE_URL') ?? env('PUBLIC_SITE_URL');
  if (!configured) return undefined;
  try {
    const url = new URL(configured);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return undefined;
    return url.origin;
  } catch {
    return undefined;
  }
}

/**
 * Public origin used for canonical URLs, the sitemap and robots.txt.
 *
 * `SITE_URL` is strongly recommended in production. Without it the request
 * origin is used, preferring the proxy's forwarded headers over the internal
 * host that the Node adapter reports.
 */
export function getSiteOrigin(requestUrl?: URL, headers?: Headers): string {
  const configured = configuredOrigin();
  if (configured) return configured;

  const forwardedHost = headers?.get('x-forwarded-host')?.split(',')[0]?.trim();
  const host = forwardedHost || headers?.get('host')?.split(',')[0]?.trim();
  const forwardedProto = headers
    ?.get('x-forwarded-proto')
    ?.split(',')[0]
    ?.trim()
    .toLowerCase();
  const protocol =
    forwardedProto === 'https' || forwardedProto === 'http'
      ? forwardedProto
      : requestUrl?.protocol.replace(':', '') || 'http';

  if (host) {
    try {
      return new URL(`${protocol}://${host}`).origin;
    } catch {
      // Ignore malformed host headers and use the URL below.
    }
  }

  return requestUrl?.origin || DEFAULT_ORIGIN;
}

function absoluteImage(value: string | undefined, origin: string): string | undefined {
  if (!value) return undefined;
  try {
    return new URL(value, `${origin}/`).toString();
  } catch {
    return undefined;
  }
}

/**
 * Whether a valid `SITE_URL` is configured. When it is not, origin-derived
 * responses such as the sitemap must not be cached publicly: a spoofed Host
 * header could otherwise poison a shared cache.
 */
export function isSiteUrlConfigured(): boolean {
  return configuredOrigin() !== undefined;
}

/** Site-wide easeo configuration resolved at request time. */
export function getSeoConfig(requestUrl?: URL, headers?: Headers): SEOConfig {
  const site = getSiteConfig();
  const origin = getSiteOrigin(requestUrl, headers);
  const parsed = new URL(origin);
  const twitter = env('SITE_TWITTER');
  const ogImage = absoluteImage(env('SITE_OG_IMAGE') ?? '/og.png', origin);

  return {
    canonicalHost: parsed.host,
    publicBaseUrl: origin,
    siteName: site.name,
    titleTemplate: '{title}',
    defaultOgImage: ogImage,
    publisherName: site.name,
    publisherLogo: `${origin}/logo.png`,
    locale: 'en_US',
    ...(twitter ? { twitterSite: twitter } : {}),
    searchUrlTemplate: `${origin}/?q={search_term_string}`,
    enforceHttps: origin.startsWith('https://'),
    // Canonical URLs need to stay stable as filters are added and removed.
    trailingSlash: 'never',
    collapseDuplicateSlashes: true,
    stripTrackingParams: true,
    lowercasePaths: false,
    autoGenerateSchema: true,
  };
}

/** Resolves a clean route to its absolute canonical URL. */
export function absoluteUrl(route: string, requestUrl?: URL, headers?: Headers): string {
  return normalizePublicUrl(cleanRoute(route), getSeoConfig(requestUrl, headers));
}

/** Removes a query string or fragment and guarantees a leading slash. */
export function cleanRoute(route: string): string {
  const path = (route.split(/[?#]/, 1)[0] || '/').trim();
  if (!path) return '/';
  return path.startsWith('/') ? path : `/${path}`;
}

function plainText(value: string | undefined): string {
  return (value ?? '')
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

/** Truncates on code points so emoji and accents are never split. */
function clamp(value: string | undefined, max: number): string {
  const text = plainText(value);
  const characters = Array.from(text);
  if (characters.length <= max) return text;
  return `${characters.slice(0, Math.max(0, max - 1)).join('').trimEnd()}…`;
}

/**
 * Builds the complete SEO payload for a page.
 *
 * The page title and description are clamped to the lengths search engines
 * usually display. `indexable: false` uses a search-result entity and explicit
 * robots overrides so noindex is always emitted, even when a caller forgets to
 * mark the entity as a draft.
 */
export function buildPageSeo(
  input: SeoInput,
  requestUrl?: URL,
  headers?: Headers,
): PageSeo {
  const site = getSiteConfig();
  const config = getSeoConfig(requestUrl, headers);
  const route = cleanRoute(input.route);
  const indexable = input.indexable !== false;
  const followable = input.followable !== false;
  const title = clamp(
    input.title || site.title || site.name,
    input.maxTitle ?? DEFAULT_MAX_TITLE,
  );
  const description = clamp(
    input.description || site.description,
    input.maxDescription ?? DEFAULT_MAX_DESCRIPTION,
  );

  const customOgImage = env('SITE_OG_IMAGE');
  const image: SeoImage | undefined =
    input.image ??
    (config.defaultOgImage
      ? {
          url: config.defaultOgImage,
          // The bundled /og.png is exactly 1200x630; a custom image might not be.
          width: customOgImage ? undefined : 1200,
          height: customOgImage ? undefined : 630,
          alt: `${site.name} — ${site.tagline}`,
        }
      : undefined);

  const entity: SEOEntity = {
    entityType: input.entityType ?? 'page',
    title,
    excerpt: description,
    image: image?.url,
    imageWidth: image?.width,
    imageHeight: image?.height,
    imageAlt: image?.alt,
    breadcrumbs: input.breadcrumbs,
    publishedAt: input.publishedAt,
    updatedAt: input.updatedAt,
    authorName: input.authorName,
  };

  const payload = buildSeoPayload(entity, route, config, {
    robotsIndex: indexable,
    robotsFollow: followable,
    ...(indexable ? { robotsMaxImagePreview: 'large' } : {}),
  });

  return {
    payload,
    head: payload.renderHtml(),
    canonical: payload.canonical,
    robots: payload.robots,
    title: payload.title,
    description: payload.description,
    noindex: payload.robots.includes('noindex'),
    origin: getSiteOrigin(requestUrl, headers),
  };
}
