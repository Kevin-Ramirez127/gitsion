import type { APIRoute } from 'astro';
import { errorMessage, refreshRepository } from '../../lib/backend';
import { repositoryPath } from '../../lib/format';

const validPart = /^[A-Za-z0-9_.-]+$/;

export const POST: APIRoute = async ({ request, redirect }) => {
  let owner = '';
  let name = '';
  try {
    const form = await request.formData();
    const ownerValue = form.get('owner');
    const nameValue = form.get('name');
    if (typeof ownerValue === 'string') owner = ownerValue.trim();
    if (typeof nameValue === 'string') name = nameValue.trim();
  } catch {
    // Validation below reports the failure.
  }

  if (!validPart.test(owner) || !validPart.test(name)) {
    return redirect('/?error=' + encodeURIComponent('Invalid repository selection.'), 303);
  }

  const base = repositoryPath(owner, name);

  try {
    const repo = await refreshRepository(owner, name);
    return redirect(
      base + '?ok=' + encodeURIComponent(`Refreshed ${repo.fullName} from GitHub.`),
      303,
    );
  } catch (error) {
    return redirect(
      base +
        '?error=' +
        encodeURIComponent(errorMessage(error, 'Could not refresh that repository.')),
      303,
    );
  }
};
