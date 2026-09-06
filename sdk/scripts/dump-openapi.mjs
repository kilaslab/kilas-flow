/**
 * Writes the server's live OpenAPI document to the path given on the command
 * line, so a test can read the operation surface from the binary rather than
 * a hand-maintained list. Same helper family as check-types.mjs.
 */
import { dumpOpenAPISpec } from '../../scripts/openapi-spec.mjs';
const [, , outPath] = process.argv;
if (!outPath) {
	console.error('usage: node dump-openapi.mjs <out-path>');
	process.exit(2);
}

await dumpOpenAPISpec(outPath);
