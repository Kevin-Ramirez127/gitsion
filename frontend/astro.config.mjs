import { loadEnvFile } from 'node:process';
import { defineConfig } from 'astro/config';
import node from '@astrojs/node';

// Local development keeps its configuration in the repository root. Node only
// fills variables that are not already set, so shell and Docker env wins.
try {
  loadEnvFile(new URL('../.env', import.meta.url));
} catch {
  // No root .env (for example inside the Docker build context).
}

export default defineConfig({
  output: 'server',
  adapter: node({ mode: 'standalone' }),
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
