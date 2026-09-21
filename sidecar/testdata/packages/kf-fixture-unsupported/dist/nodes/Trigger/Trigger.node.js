'use strict';

// A trigger-group node with no execute() and a webhook method: v1 has no
// trigger or webhook surface, so the converter excludes it by name and the
// runner records the methods it found.

Object.defineProperty(exports, '__esModule', { value: true });
exports.Trigger = void 0;

class Trigger {
	constructor() {
		this.description = {
			displayName: 'Fixture Trigger',
			name: 'fixtureTrigger',
			group: ['trigger'],
			version: 1,
			defaults: { name: 'Fixture Trigger' },
			inputs: [],
			outputs: ['main'],
			webhooks: [{ name: 'default', httpMethod: 'POST', path: 'fixture' }],
			properties: [],
		};
	}

	async webhook() {
		return { workflowData: [this.helpers.returnJsonArray([{ received: true }])] };
	}
}

exports.Trigger = Trigger;
