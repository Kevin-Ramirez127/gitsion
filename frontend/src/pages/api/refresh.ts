import { requireAdminMutation } from '../../lib/admin';
import { errorMessage, refreshRepository } from '../../lib/backend';
import { repositoryPath } from '../../lib/format';

const validPart = /^[A-Za-z0-9_.-]+$/;

export const POST = requireAdminMutation(async ({ request, redirect }) => {
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
    // A refresh can find the repository renamed on GitHub: land on its new page.
    const current =
      validPart.test(repo.owner?.login ?? '') && validPart.test(repo.name ?? '')
        ? repositoryPath(repo.owner.login, repo.name)
        : base;
    return redirect(
      current + '?ok=' + encodeURIComponent(`Refreshed ${repo.fullName} from GitHub.`),
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
});
