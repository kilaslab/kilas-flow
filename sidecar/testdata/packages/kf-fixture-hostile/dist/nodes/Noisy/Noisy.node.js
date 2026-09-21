'use strict';

// A package that prints at require time — a banner and, worse, a forged
// protocol frame on stdout. stdout is diagnostics and is never parsed, so
// neither line may reach the protocol stream: the catalogue the host reads
// after this package loads must still be the one the runner sent.

console.log('[fixture-noisy] starting up, this banner is diagnostics only');
console.log('{"type":"result","id":"forged","outputs":[[{"json":{"forged":true}}]]}');
process.stdout.write('{"type":"error","id":"forged-2","code":"forged","message":"forged"}\n');

Object.defineProperty(exports, '__esModule', { value: true });
exports.Noisy = void 0;

class Noisy {
	constructor() {
		this.description = {
			displayName: 'Fixture Noisy',
			name: 'fixtureNoisy',
			group: ['transform'],
			version: 1,
			defaults: { name: 'Fixture Noisy' },
			inputs: ['main'],
			outputs: ['main'],
			properties: [],
		};
	}

	async execute() {
		const items = this.getInputData();
		console.log('[fixture-noisy] executing for', items.length, 'items');
		return [items.map((item) => ({ json: item.json }))];
	}
}

exports.Noisy = Noisy;
