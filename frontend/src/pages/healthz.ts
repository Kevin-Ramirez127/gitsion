import type { APIRoute } from 'astro';

export const GET: APIRoute = () =>
  new Response('ok\n', {
    status: 200,
    headers: {
      'Content-Type': 'text/plain; charset=utf-8',
      'Cache-Control': 'no-store',
    },
  });
