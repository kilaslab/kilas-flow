export function renameKeyValue(values: Record<string, unknown>, previousKey: string, nextKey: string): Record<string, unknown> {
	const next = { ...values };
	const value = next[previousKey];
	delete next[previousKey];
	if (nextKey.trim()) next[nextKey] = value;
	return next;
}
