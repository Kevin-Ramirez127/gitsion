import type { APIRoute } from 'astro';
import { errorMessage, untrackRepository } from '../../lib/backend';
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

  try {
    await untrackRepository(owner, name);
    return redirect(
      '/?ok=' + encodeURIComponent(`Stopped tracking ${owner}/${name}.`),
      303,
    );
  } catch (error) {
    const target =
      repositoryPath(owner, name) +
      '?error=' +
      encodeURIComponent(errorMessage(error, 'Could not remove that repository.'));
    return redirect(target, 303);
  }
};
