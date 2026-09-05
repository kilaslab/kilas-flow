import { defineCollection } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

// Only the `docs` collection. Starlight also looks for an `i18n` collection and
// warns on every build that it is missing; declaring an empty one does not
// silence that and adds a second warning about the absent directory, so the site
// stays single-collection until it is actually translated.
export const collections = {
	docs: defineCollection({ loader: docsLoader(), schema: docsSchema() }),
};
