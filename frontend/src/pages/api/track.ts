import type { APIRoute } from 'astro';
import { errorMessage, trackRepository } from '../../lib/backend';
import { repositoryPath } from '../../lib/format';

export const POST: APIRoute = async ({ request, redirect }) => {
  let repository = '';
  try {
    const form = await request.formData();
    const value = form.get('repository');
    if (typeof value === 'string') repository = value.trim();
  } catch {
    // Handled as an empty value below.
  }

  if (!repository) {
    return redirect('/?error=' + encodeURIComponent('Enter a repository to track.'), 303);
  }

  try {
    const repo = await trackRepository(repository);
    const target =
      repositoryPath(repo.owner.login, repo.name) +
      '?ok=' +
      encodeURIComponent(`Now tracking ${repo.fullName}.`);
    return redirect(target, 303);
  } catch (error) {
    return redirect(
      '/?error=' + encodeURIComponent(errorMessage(error, 'Could not track that repository.')),
      303,
    );
  }
};
