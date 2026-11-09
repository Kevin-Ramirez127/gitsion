import { loadEnvFile } from 'node:process';
import { defineConfig } from 'astro/config';
import node from '@astrojs/node';
import easeo from '@easeo/astro';

// Local development keeps its configuration in the repository root. Node only
// fills variables that are not already set, so shell and Docker env wins.
try {
  loadEnvFile(new URL('../.env', import.meta.url));
} catch {
  // No root .env (for example inside the Docker build context).
}

function parseSiteUrl(value) {
  if (!value) return undefined;
  try {
    const url = new URL(value.trim());
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return undefined;
    return url;
  } catch {
    return undefined;
  }
}

const configuredSite = parseSiteUrl(process.env.SITE_URL);
// Build-time fallback only. Runtime canonical URLs use SITE_URL again and fall
// back to the request origin, so a missing value at build time is harmless.
const siteUrl = configuredSite ?? new URL('http://localhost:4321');
const siteName = process.env.SITE_NAME?.trim() || 'gitsion';

// The integration injects the SEO config and, when SITE_URL is set, emits a
// machine-readable contract to the build output after the build.
const seoIntegration = easeo({
  config: {
    canonicalHost: siteUrl.host,
    publicBaseUrl: siteUrl.origin,
    siteName,
    titleTemplate: '{title}',
    enforceHttps: siteUrl.protocol === 'https:',
    trailingSlash: 'never',
  },
  ...(configuredSite
    ? {
        contract: {
          canonicalHost: siteUrl.host,
          scheme: siteUrl.protocol.replace(':', ''),
          defaults: {
            indexable: true,
            canonical: 'self',
            title: { required: true },
            description: { required: true },
            ogRequired: true,
            twitterRequired: true,
            schemaRequired: true,
          },
          rules: [
            {
              match: '/',
              expect: {
                indexable: true,
                schemaRequired: true,
                schemaTypes: ['WebSite'],
              },
            },
            {
              match: '/issues',
              expect: {
                indexable: true,
                schemaRequired: true,
                schemaTypes: ['CollectionPage'],
              },
            },
            {
              match: '/pulls',
              expect: {
                indexable: true,
                schemaRequired: true,
                schemaTypes: ['CollectionPage'],
              },
            },
            {
              match: '/repos/*',
              expect: {
                schemaRequired: true,
                schemaTypes: ['WebPage', 'BreadcrumbList'],
              },
            },
            { match: '/admin*', expect: { indexable: false } },
          ],
        },
      }
    : {}),
});

export default defineConfig({
  ...(configuredSite ? { site: configuredSite.origin } : {}),
  output: 'server',
  adapter: node({ mode: 'standalone' }),
  integrations: [seoIntegration],
  server: {
    host: true,
    port: 4321,
  },
  // Astro's built-in origin check sees the internal Host header behind a TLS
  // proxy. The middleware checks Fetch Metadata instead and enforces the admin
  // session on every mutation.
  security: {
    checkOrigin: false,
  },
  devToolbar: {
    enabled: false,
  },
});
