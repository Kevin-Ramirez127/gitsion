/**
 * Runtime SEO payload builder for Cloudflare Workers.
 *
 * easeo ships as a napi-rs native module, which `workerd` cannot load. The
 * build-time Astro integration still uses easeo (Node); this module implements
 * the small subset of `@easeo/core` that the app needs at request time, so the
 * Worker bundle stays JavaScript-only.
 */

export interface Breadcrumb {
  name: string;
  url?: string;
}

export interface SEOConfig {
  canonicalHost: string;
  publicBaseUrl: string;
  siteName: string;
  titleTemplate?: string;
  defaultOgImage?: string;
  publisherName?: string;
  publisherLogo?: string;
  locale?: string;
  twitterSite?: string;
  searchUrlTemplate?: string;
  enforceHttps?: boolean;
  trailingSlash?: 'always' | 'never';
  collapseDuplicateSlashes?: boolean;
  stripTrackingParams?: boolean;
  lowercasePaths?: boolean;
  autoGenerateSchema?: boolean;
}

export interface SEOEntity {
  entityType: string;
  title: string;
  excerpt?: string;
  image?: string;
  imageWidth?: number;
  imageHeight?: number;
  imageAlt?: string;
  breadcrumbs?: Breadcrumb[];
  publishedAt?: string;
  updatedAt?: string;
  authorName?: string;
}

export interface SEOOverrides {
  robotsIndex?: boolean;
  robotsFollow?: boolean;
  robotsMaxImagePreview?: string;
}

export interface SEOPayload {
  title: string;
  description: string;
  canonical: string;
  robots: string;
  openGraph: Record<string, string>;
  twitter: Record<string, string>;
  schemaJsonLd?: unknown;
  renderHtml(): string;
}

function escapeHtml(value: string): string {
  return value.replace(
    /[&<>"']/g,
    (char) =>
      ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;',
      })[char] ?? char,
  );
}

function absoluteUrl(value: string, canonical: string): string {
  try {
    return new URL(value, canonical).toString();
  } catch {
    return value;
  }
}

export function normalizePublicUrl(route: string, config: SEOConfig): string {
  const base = config.publicBaseUrl;
  let path = route.startsWith('/') ? route : `/${route}`;
  if (config.collapseDuplicateSlashes !== false) {
    path = path.replace(/\/{2,}/g, '/');
  }
  if (config.trailingSlash === 'never' && path !== '/') {
    path = path.replace(/\/+$/, '');
  } else if (config.trailingSlash === 'always' && !path.endsWith('/')) {
    path += '/';
  }
  if (config.lowercasePaths) {
    path = path.toLowerCase();
  }

  const url = new URL(path, base);
  if (config.stripTrackingParams !== false) {
    for (const key of [...url.searchParams.keys()]) {
      if (key.startsWith('utm_') || key === 'fbclid' || key === 'gclid') {
        url.searchParams.delete(key);
      }
    }
  }
  return url.toString();
}

function buildSchema(
  entity: SEOEntity,
  config: SEOConfig,
  canonical: string,
  description: string,
  image: string | undefined,
): unknown {
  const site = {
    '@type': 'WebSite',
    name: config.siteName,
    url: config.publicBaseUrl,
  };
  const base = {
    name: entity.title,
    description,
    url: canonical,
    ...(image ? { image } : {}),
    ...(entity.updatedAt ? { dateModified: entity.updatedAt } : {}),
    ...(entity.publishedAt ? { datePublished: entity.publishedAt } : {}),
  };

  if (entity.entityType === 'home') {
    return {
      '@context': 'https://schema.org',
      ...site,
      potentialAction: {
        '@type': 'SearchAction',
        target: config.searchUrlTemplate ?? `${config.publicBaseUrl}/?q={search_term_string}`,
        'query-input': 'required name=search_term_string',
      },
    };
  }

  if (entity.entityType === 'taxonomy') {
    return {
      '@context': 'https://schema.org',
      '@type': 'CollectionPage',
      ...base,
      isPartOf: site,
    };
  }

  if (entity.breadcrumbs?.length) {
    return {
      '@context': 'https://schema.org',
      '@graph': [
        { '@type': 'WebPage', ...base },
        {
          '@type': 'BreadcrumbList',
          itemListElement: entity.breadcrumbs.map((crumb, index) => ({
            '@type': 'ListItem',
            position: index + 1,
            name: crumb.name,
            ...(crumb.url ? { item: absoluteUrl(crumb.url, canonical) } : {}),
          })),
        },
      ],
    };
  }

  return {
    '@context': 'https://schema.org',
    '@type': 'WebPage',
    ...base,
    isPartOf: site,
  };
}

function renderHtml(payload: SEOPayload): string {
  const tags = [
    `<title>${escapeHtml(payload.title)}</title>`,
    `<meta name="description" content="${escapeHtml(payload.description)}">`,
    `<link rel="canonical" href="${escapeHtml(payload.canonical)}">`,
    `<meta name="robots" content="${escapeHtml(payload.robots)}">`,
    ...Object.entries(payload.openGraph).map(
      ([property, content]) =>
        `<meta property="${escapeHtml(property)}" content="${escapeHtml(content)}">`,
    ),
    ...Object.entries(payload.twitter).map(
      ([name, content]) =>
        `<meta name="${escapeHtml(name)}" content="${escapeHtml(content)}">`,
    ),
  ];

  if (payload.schemaJsonLd) {
    tags.push(
      `<script type="application/ld+json">${JSON.stringify(payload.schemaJsonLd).replace(
        /</g,
        '\\u003c',
      )}</script>`,
    );
  }

  return tags.join('\n');
}

export function buildSeoPayload(
  entity: SEOEntity,
  route: string,
  config: SEOConfig,
  overrides: SEOOverrides = {},
): SEOPayload {
  const canonical = normalizePublicUrl(route, config);
  const description = entity.excerpt ?? '';
  const image = entity.image ? absoluteUrl(entity.image, canonical) : undefined;
  const index = overrides.robotsIndex !== false;
  const follow = overrides.robotsFollow !== false;
  const robots = [
    index ? 'index' : 'noindex',
    follow ? 'follow' : 'nofollow',
    ...(index && overrides.robotsMaxImagePreview
      ? [`max-image-preview:${overrides.robotsMaxImagePreview}`]
      : []),
  ].join(',');

  const openGraph: Record<string, string> = {
    'og:type': entity.entityType === 'home' ? 'website' : 'article',
    'og:site_name': config.siteName,
    'og:title': entity.title,
    'og:description': description,
    'og:url': canonical,
    'og:locale': config.locale ?? 'en_US',
  };
  if (image) {
    openGraph['og:image'] = image;
    if (entity.imageWidth) openGraph['og:image:width'] = String(entity.imageWidth);
    if (entity.imageHeight) openGraph['og:image:height'] = String(entity.imageHeight);
    if (entity.imageAlt) openGraph['og:image:alt'] = entity.imageAlt;
  }
  if (entity.publishedAt) openGraph['article:published_time'] = entity.publishedAt;
  if (entity.updatedAt) openGraph['article:modified_time'] = entity.updatedAt;

  const twitter: Record<string, string> = {
    'twitter:card': image ? 'summary_large_image' : 'summary',
    'twitter:title': entity.title,
    'twitter:description': description,
    ...(image ? { 'twitter:image': image } : {}),
    ...(config.twitterSite ? { 'twitter:site': config.twitterSite } : {}),
  };

  const payload: SEOPayload = {
    title: entity.title,
    description,
    canonical,
    robots,
    openGraph,
    twitter,
    schemaJsonLd: config.autoGenerateSchema === false
      ? undefined
      : buildSchema(entity, config, canonical, description, image),
    renderHtml() {
      return renderHtml(this);
    },
  };

  return payload;
}
