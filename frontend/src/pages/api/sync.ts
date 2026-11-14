import { requireAdminMutation } from '../../lib/admin';
import { errorMessage, syncAll } from '../../lib/backend';

export const POST = requireAdminMutation(async ({ redirect }) => {
  try {
    await syncAll();
    return redirect(
      '/?ok=' + encodeURIComponent('A sync of all tracked repositories has started.'),
      303,
    );
  } catch (error) {
    return redirect(
      '/?error=' + encodeURIComponent(errorMessage(error, 'Could not start a sync.')),
      303,
    );
  }
});
