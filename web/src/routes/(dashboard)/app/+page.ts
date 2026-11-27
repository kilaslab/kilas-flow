import { redirect } from '@sveltejs/kit';

// /app on its own is the dashboard's root, so it opens the workflow list
// rather than a 404 (FEAT-p01rcw).
export function load() {
	redirect(307, '/app/workflows');
}
