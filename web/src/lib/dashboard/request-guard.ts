/**
 * Tells a request that has just resolved whether anyone is still waiting for
 * it.
 *
 * A filtered list issues a new request every time a filter changes, and the
 * network is free to answer them out of order. Without a guard the slower
 * earlier request wins simply by finishing last, and the page shows the
 * previous filter's rows under the new filter's controls. Nothing throws and
 * nothing looks broken — the count is just quietly wrong — which is why this
 * is a tested module and not an inline comparison in a component.
 *
 * A counter rather than AbortController: the earlier request's response is
 * still worth nothing to us, but aborting mid-flight loses the ability to
 * distinguish "superseded" from "the user's network failed", and the two need
 * different treatment at the call site.
 */
export class RequestGuard {
	#issued = 0;

	/**
	 * Supersedes anything in flight and returns the token the new request must
	 * present when it resolves.
	 */
	start(): number {
		return ++this.#issued;
	}

	/**
	 * The token of the request the user is currently waiting on.
	 *
	 * A follow-on page — "Load more" — reads this instead of calling start().
	 * Starting would supersede the very request whose results it is extending,
	 * so that request would discard its own answer on arrival and leave the
	 * page pinned in its loading state with nothing left to clear it.
	 */
	get current(): number {
		return this.#issued;
	}

	/** Whether a resolved request is still the one the user is waiting for. */
	holds(token: number): boolean {
		return token === this.#issued;
	}
}
