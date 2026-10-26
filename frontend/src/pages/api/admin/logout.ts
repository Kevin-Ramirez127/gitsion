import type { APIRoute } from 'astro';
import { ADMIN_COOKIE } from '../../../lib/auth';

export const POST: APIRoute = async ({ cookies, redirect }) => {
  cookies.delete(ADMIN_COOKIE, { path: '/' });
  return redirect('/?ok=' + encodeURIComponent('Signed out.'), 303);
};
