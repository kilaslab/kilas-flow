import { redirect } from '@sveltejs/kit';

// The root has no page of its own: a first visit lands on the workflow list,
// the product's home (FEAT-p01rcw). The instance's status and version live
// under Settings → About this instance. With authentication on, the dashboard
// layout sends a signed-out visitor on to /login from there.
export function load() {
	redirect(307, '/app/workflows');
}
