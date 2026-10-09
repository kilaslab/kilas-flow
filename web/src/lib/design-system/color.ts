// A small CSS colour engine for the design-token tests and the theme-color
// meta. It understands exactly the colour syntax web/src/app.css uses, and
// throws on anything else rather than guessing:
//
//   - hex (#rgb, #rgba, #rrggbb, #rrggbbaa), rgb()/rgba(), color(srgb …),
//     `transparent`;
//   - absolute oklch(L C H [/ A]);
//   - relative oklch(from <colour> <l> <c> <h> [/ <alpha>]), whose channels may
//     be numbers, the keywords l/c/h/alpha, + - * /, parentheses and the
//     calc/min/max/clamp math functions;
//   - color-mix(in oklch, A [p%], B [q%]), including `transparent` as either side.
//
// Colours are carried in OKLCH without clipping, as browsers carry the origin
// of a relative colour, and are clipped per channel only on the way to sRGB.
// Compositing happens in gamma-encoded sRGB, which is how a browser paints one
// translucent layer over another. No dependencies: e2e specs import this file
// directly.

export interface Oklch {
	l: number;
	c: number;
	/** Degrees in [0, 360); NaN when the hue is powerless (achromatic or `none`). */
	h: number;
	alpha: number;
}

export interface Srgb {
	/** Gamma-encoded channels in [0, 1]. */
	r: number;
	g: number;
	b: number;
	alpha: number;
}

export class ColorParseError extends Error {}

// ---------------------------------------------------------------------------
// conversions (Björn Ottosson's OKLab matrices, the ones CSS Color 4 uses)

function srgbToLinear(v: number): number {
	const a = Math.abs(v);
	const out = a <= 0.04045 ? a / 12.92 : Math.pow((a + 0.055) / 1.055, 2.4);
	return Math.sign(v) * out;
}

function linearToSrgb(v: number): number {
	const a = Math.abs(v);
	const out = a <= 0.0031308 ? 12.92 * a : 1.055 * Math.pow(a, 1 / 2.4) - 0.055;
	return Math.sign(v) * out;
}

const ACHROMATIC = 1e-4;

export function srgbToOklch(rgb: Srgb): Oklch {
	const r = srgbToLinear(rgb.r);
	const g = srgbToLinear(rgb.g);
	const b = srgbToLinear(rgb.b);
	const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
	const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
	const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
	const L = 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s;
	const A = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
	const B = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
	const C = Math.hypot(A, B);
	const h = C < ACHROMATIC ? NaN : normalizeHue((Math.atan2(B, A) * 180) / Math.PI);
	return { l: L, c: C, h, alpha: rgb.alpha };
}

function oklchToLinear(color: Oklch): [number, number, number] {
	const hue = Number.isNaN(color.h) ? 0 : (color.h * Math.PI) / 180;
	const A = color.c * Math.cos(hue);
	const B = color.c * Math.sin(hue);
	const l = Math.pow(color.l + 0.3963377774 * A + 0.2158037573 * B, 3);
	const m = Math.pow(color.l - 0.1055613458 * A - 0.0638541728 * B, 3);
	const s = Math.pow(color.l - 0.0894841775 * A - 1.291485548 * B, 3);
	return [
		4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
		-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
		-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s
	];
}

const clip01 = (v: number) => Math.min(1, Math.max(0, v));

/** OKLCH to gamma-encoded sRGB, clipped per channel like Chromium does. */
export function toSrgb(color: Oklch | Srgb): Srgb {
	if ('r' in color) return color;
	const [r, g, b] = oklchToLinear(color);
	return { r: clip01(linearToSrgb(r)), g: clip01(linearToSrgb(g)), b: clip01(linearToSrgb(b)), alpha: color.alpha };
}

export function toHex(color: Oklch | Srgb): string {
	const rgb = toSrgb(color);
	const hex = (v: number) => Math.round(clip01(v) * 255).toString(16).padStart(2, '0');
	const base = `#${hex(rgb.r)}${hex(rgb.g)}${hex(rgb.b)}`;
	return rgb.alpha < 1 ? base + hex(rgb.alpha) : base;
}

export function relativeLuminance(color: Oklch | Srgb): number {
	const rgb = toSrgb(color);
	return 0.2126 * srgbToLinear(rgb.r) + 0.7152 * srgbToLinear(rgb.g) + 0.0722 * srgbToLinear(rgb.b);
}

/** WCAG 2 contrast ratio of two opaque colours (alpha is ignored; composite first). */
export function contrast(a: Oklch | Srgb, b: Oklch | Srgb): number {
	const la = relativeLuminance(a);
	const lb = relativeLuminance(b);
	return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

/** Paints `top` (with its alpha) over `bottom` in gamma sRGB, as a browser does. */
export function composite(top: Oklch | Srgb, bottom: Oklch | Srgb): Srgb {
	const t = toSrgb(top);
	const b = toSrgb(bottom);
	const a = t.alpha + b.alpha * (1 - t.alpha);
	if (a === 0) return { r: 0, g: 0, b: 0, alpha: 0 };
	const mix = (x: number, y: number) => (x * t.alpha + y * b.alpha * (1 - t.alpha)) / a;
	return { r: mix(t.r, b.r), g: mix(t.g, b.g), b: mix(t.b, b.b), alpha: a };
}

/** Euclidean distance in OKLab (ΔE_OK). */
export function deltaEOK(x: Oklch, y: Oklch): number {
	const lab = (c: Oklch) => {
		const h = Number.isNaN(c.h) ? 0 : (c.h * Math.PI) / 180;
		return [c.l, c.c * Math.cos(h), c.c * Math.sin(h)];
	};
	const [l1, a1, b1] = lab(x);
	const [l2, a2, b2] = lab(y);
	return Math.hypot(l1 - l2, a1 - a2, b1 - b2);
}

export function normalizeHue(h: number): number {
	const out = h % 360;
	return out < 0 ? out + 360 : out;
}

/** Shortest angular distance between two hues, in degrees (0–180). */
export function hueDistance(a: number, b: number): number {
	const d = Math.abs(normalizeHue(a) - normalizeHue(b));
	return d > 180 ? 360 - d : d;
}

// ---------------------------------------------------------------------------
// parsing

/** Splits on top-level separators (whitespace or commas), keeping nested parentheses intact. */
function splitTopLevel(text: string, separator: 'space' | 'comma'): string[] {
	const parts: string[] = [];
	let depth = 0;
	let current = '';
	for (const ch of text) {
		if (ch === '(') depth += 1;
		if (ch === ')') depth -= 1;
		const isSep = separator === 'comma' ? ch === ',' : /\s/.test(ch);
		if (depth === 0 && isSep) {
			if (current.trim() !== '' || separator === 'comma') parts.push(current.trim());
			current = '';
			continue;
		}
		current += ch;
	}
	if (current.trim() !== '' || (separator === 'comma' && parts.length > 0)) parts.push(current.trim());
	return parts;
}

/** Returns [name, inner] for `name( inner )` spanning the whole text, or null. */
function functionCall(text: string): [string, string] | null {
	const match = /^([a-z-]+)\(/i.exec(text);
	if (!match || !text.endsWith(')')) return null;
	let depth = 0;
	for (let i = match[0].length - 1; i < text.length; i += 1) {
		if (text[i] === '(') depth += 1;
		if (text[i] === ')') {
			depth -= 1;
			if (depth === 0 && i !== text.length - 1) return null;
		}
	}
	return [match[1].toLowerCase(), text.slice(match[0].length, -1).trim()];
}

function parseHex(text: string): Oklch {
	const hex = text.slice(1);
	if (!/^([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.test(hex)) {
		throw new ColorParseError(`unsupported hex colour: ${text}`);
	}
	const full = hex.length <= 4 ? [...hex].map((ch) => ch + ch).join('') : hex;
	const channel = (i: number) => parseInt(full.slice(i, i + 2), 16) / 255;
	return srgbToOklch({ r: channel(0), g: channel(2), b: channel(4), alpha: full.length === 8 ? channel(6) : 1 });
}

function parseAlphaLiteral(text: string): number {
	return text.endsWith('%') ? Number.parseFloat(text) / 100 : Number(text);
}

function requireNumber(value: number, text: string, context: string): number {
	if (!Number.isFinite(value)) throw new ColorParseError(`${context}: not a number: ${text}`);
	return value;
}

/** Splits `a b c / d` into channel texts and an optional alpha text. */
function channelsAndAlpha(inner: string): { channels: string[]; alpha?: string } {
	const parts = splitTopLevel(inner, 'space');
	const slash = parts.indexOf('/');
	if (slash === -1) return { channels: parts };
	return { channels: parts.slice(0, slash), alpha: parts.slice(slash + 1).join(' ') };
}

function parseRgb(inner: string): Oklch {
	const commaParts = splitTopLevel(inner, 'comma');
	let channels: string[];
	let alpha: string | undefined;
	if (commaParts.length > 1) {
		channels = commaParts.slice(0, 3);
		alpha = commaParts[3];
	} else {
		({ channels, alpha } = channelsAndAlpha(inner));
	}
	if (channels.length !== 3) throw new ColorParseError(`rgb() needs three channels: rgb(${inner})`);
	const value = (text: string) =>
		requireNumber(text.endsWith('%') ? Number.parseFloat(text) / 100 : Number(text) / 255, text, 'rgb()');
	return srgbToOklch({
		r: value(channels[0]),
		g: value(channels[1]),
		b: value(channels[2]),
		alpha: alpha === undefined ? 1 : requireNumber(parseAlphaLiteral(alpha), alpha, 'rgb() alpha')
	});
}

/** color(srgb r g b [/ a]): how a browser may serialise a computed colour. */
function parseColorFunction(inner: string): Oklch {
	const { channels, alpha } = channelsAndAlpha(inner);
	if (channels[0] !== 'srgb' || channels.length !== 4) {
		throw new ColorParseError(`unsupported color() space (only srgb): color(${inner})`);
	}
	const value = (text: string) =>
		requireNumber(text.endsWith('%') ? Number.parseFloat(text) / 100 : Number(text), text, 'color(srgb)');
	return srgbToOklch({
		r: value(channels[1]),
		g: value(channels[2]),
		b: value(channels[3]),
		alpha: alpha === undefined ? 1 : requireNumber(parseAlphaLiteral(alpha), alpha, 'color() alpha')
	});
}

function parseOklch(inner: string): Oklch {
	if (inner.startsWith('from ')) return parseRelativeOklch(inner.slice(5).trim());
	const { channels, alpha } = channelsAndAlpha(inner);
	if (channels.length !== 3) throw new ColorParseError(`oklch() needs three channels: oklch(${inner})`);
	const l = channels[0].endsWith('%') ? Number.parseFloat(channels[0]) / 100 : Number(channels[0]);
	const c = channels[1].endsWith('%') ? (Number.parseFloat(channels[1]) / 100) * 0.4 : Number(channels[1]);
	const h = channels[2] === 'none' ? NaN : normalizeHue(Number(channels[2]));
	requireNumber(l, channels[0], 'oklch() L');
	requireNumber(c, channels[1], 'oklch() C');
	if (channels[2] !== 'none') requireNumber(h, channels[2], 'oklch() H');
	return {
		l,
		c,
		h: c < ACHROMATIC && channels[2] === 'none' ? NaN : h,
		alpha: alpha === undefined ? 1 : requireNumber(parseAlphaLiteral(alpha), alpha, 'oklch() alpha')
	};
}

function parseRelativeOklch(rest: string): Oklch {
	const parts = splitTopLevel(rest, 'space');
	const originText = parts[0];
	const { channels, alpha } = channelsAndAlpha(parts.slice(1).join(' '));
	if (channels.length !== 3) throw new ColorParseError(`relative oklch() needs three channels: oklch(from ${rest})`);
	const origin = parseCssColor(originText);
	const scope: Record<string, number> = {
		l: origin.l,
		c: origin.c,
		// A powerless origin hue resolves to 0 in relative syntax.
		h: Number.isNaN(origin.h) ? 0 : origin.h,
		alpha: origin.alpha
	};
	const l = evaluateChannel(channels[0], scope);
	const c = Math.max(0, evaluateChannel(channels[1], scope));
	const h = normalizeHue(evaluateChannel(channels[2], scope));
	return {
		l,
		c,
		h: c < ACHROMATIC && Number.isNaN(origin.h) ? NaN : h,
		alpha: alpha === undefined ? origin.alpha : evaluateChannel(alpha, scope, true)
	};
}

function parseColorMix(inner: string): Oklch {
	const parts = splitTopLevel(inner, 'comma');
	if (parts.length !== 3 || parts[0].replace(/\s+/g, ' ') !== 'in oklch') {
		throw new ColorParseError(`unsupported color-mix (only "in oklch" with two colours): color-mix(${inner})`);
	}
	const side = (text: string): { color: Oklch; pct?: number } => {
		const tokens = splitTopLevel(text, 'space');
		const pctIndex = tokens.findIndex((t) => /^-?[\d.]+%$/.test(t));
		const pct = pctIndex === -1 ? undefined : Number.parseFloat(tokens[pctIndex]) / 100;
		const colorText = tokens.filter((_, i) => i !== pctIndex).join(' ');
		return { color: parseCssColor(colorText), pct };
	};
	const a = side(parts[1]);
	const b = side(parts[2]);
	let p1 = a.pct;
	let p2 = b.pct;
	if (p1 === undefined && p2 === undefined) {
		p1 = 0.5;
		p2 = 0.5;
	} else if (p1 === undefined) {
		p1 = 1 - (p2 as number);
	} else if (p2 === undefined) {
		p2 = 1 - p1;
	}
	const sum = (p1 as number) + (p2 as number);
	if (sum <= 0) throw new ColorParseError(`color-mix weights sum to zero: color-mix(${inner})`);
	const alphaMultiplier = Math.min(1, sum);
	const w1 = (p1 as number) / sum;
	const w2 = (p2 as number) / sum;

	// Premultiplied interpolation (CSS Color 4 §12.3). A missing hue takes the
	// other side's; `transparent` is oklch(0 0 none / 0).
	const alpha = a.color.alpha * w1 + b.color.alpha * w2;
	const premul = (va: number, vb: number) => va * a.color.alpha * w1 + vb * b.color.alpha * w2;
	const l = alpha === 0 ? 0 : premul(a.color.l, b.color.l) / alpha;
	const c = alpha === 0 ? 0 : premul(a.color.c, b.color.c) / alpha;
	let h: number;
	const ha = a.color.h;
	const hb = b.color.h;
	if (Number.isNaN(ha) && Number.isNaN(hb)) h = NaN;
	else if (Number.isNaN(ha)) h = hb;
	else if (Number.isNaN(hb)) h = ha;
	else {
		let delta = hb - ha;
		if (delta > 180) delta -= 360;
		if (delta < -180) delta += 360;
		h = normalizeHue(ha + delta * w2);
	}
	return { l, c, h, alpha: alpha * alphaMultiplier };
}

/** Parses one CSS colour value (no var(); substitute those first). */
export function parseCssColor(input: string): Oklch {
	const text = input.trim().replace(/\s+/g, ' ');
	if (text.includes('var(')) throw new ColorParseError(`unresolved var() in colour: ${text}`);
	if (text === 'transparent') return { l: 0, c: 0, h: NaN, alpha: 0 };
	if (text === 'white') return { l: 1, c: 0, h: NaN, alpha: 1 };
	if (text === 'black') return { l: 0, c: 0, h: NaN, alpha: 1 };
	if (text.startsWith('#')) return parseHex(text);
	const call = functionCall(text);
	if (!call) throw new ColorParseError(`unsupported colour syntax: ${text}`);
	const [name, inner] = call;
	switch (name) {
		case 'oklch':
			return parseOklch(inner);
		case 'rgb':
		case 'rgba':
			return parseRgb(inner);
		case 'color-mix':
			return parseColorMix(inner);
		case 'color':
			return parseColorFunction(inner);
		default:
			throw new ColorParseError(`unsupported colour function ${name}(): ${text}`);
	}
}

// ---------------------------------------------------------------------------
// channel expressions for relative colour syntax

type Token = { kind: 'num'; value: number } | { kind: 'ident'; value: string } | { kind: 'op'; value: string };

function tokenize(text: string): Token[] {
	const tokens: Token[] = [];
	let i = 0;
	while (i < text.length) {
		const ch = text[i];
		if (/\s/.test(ch)) {
			i += 1;
			continue;
		}
		const num = /^(\d+\.?\d*|\.\d+)(e[+-]?\d+)?%?/i.exec(text.slice(i));
		if (num) {
			const raw = num[0];
			const value = raw.endsWith('%') ? Number.parseFloat(raw) / 100 : Number(raw);
			tokens.push({ kind: 'num', value });
			i += raw.length;
			continue;
		}
		const ident = /^[a-z][a-z-]*/i.exec(text.slice(i));
		if (ident) {
			tokens.push({ kind: 'ident', value: ident[0].toLowerCase() });
			i += ident[0].length;
			continue;
		}
		if ('+-*/(),'.includes(ch)) {
			tokens.push({ kind: 'op', value: ch });
			i += 1;
			continue;
		}
		throw new ColorParseError(`unexpected "${ch}" in channel expression: ${text}`);
	}
	return tokens;
}

/** Evaluates one channel: a number, a channel keyword or a calc()-style expression. */
export function evaluateChannel(text: string, scope: Record<string, number>, isAlpha = false): number {
	if (isAlpha && /^[\d.]+%$/.test(text.trim())) return Number.parseFloat(text) / 100;
	if (text.trim() === 'none') return NaN;
	const tokens = tokenize(text);
	let pos = 0;
	const peek = () => tokens[pos];
	const take = () => tokens[pos++];
	const expectOp = (value: string) => {
		const t = take();
		if (!t || t.kind !== 'op' || t.value !== value) {
			throw new ColorParseError(`expected "${value}" in channel expression: ${text}`);
		}
	};

	const parseSum = (): number => {
		let value = parseProduct();
		for (;;) {
			const t = peek();
			if (t?.kind === 'op' && (t.value === '+' || t.value === '-')) {
				take();
				const rhs = parseProduct();
				value = t.value === '+' ? value + rhs : value - rhs;
			} else return value;
		}
	};
	const parseProduct = (): number => {
		let value = parseUnary();
		for (;;) {
			const t = peek();
			if (t?.kind === 'op' && (t.value === '*' || t.value === '/')) {
				take();
				const rhs = parseUnary();
				value = t.value === '*' ? value * rhs : value / rhs;
			} else return value;
		}
	};
	const parseUnary = (): number => {
		const t = peek();
		if (t?.kind === 'op' && (t.value === '-' || t.value === '+')) {
			take();
			const v = parseUnary();
			return t.value === '-' ? -v : v;
		}
		return parsePrimary();
	};
	const parseArgs = (): number[] => {
		expectOp('(');
		const args = [parseSum()];
		while (peek()?.kind === 'op' && peek()?.value === ',') {
			take();
			args.push(parseSum());
		}
		expectOp(')');
		return args;
	};
	const parsePrimary = (): number => {
		const t = take();
		if (!t) throw new ColorParseError(`unexpected end of channel expression: ${text}`);
		if (t.kind === 'num') return t.value;
		if (t.kind === 'op' && t.value === '(') {
			const v = parseSum();
			expectOp(')');
			return v;
		}
		if (t.kind === 'ident') {
			const next = peek();
			if (next?.kind === 'op' && next.value === '(') {
				const args = parseArgs();
				switch (t.value) {
					case 'calc':
						if (args.length !== 1) break;
						return args[0];
					case 'min':
						return Math.min(...args);
					case 'max':
						return Math.max(...args);
					case 'clamp':
						if (args.length !== 3) break;
						return Math.max(args[0], Math.min(args[1], args[2]));
				}
				throw new ColorParseError(`unsupported math function ${t.value}() in: ${text}`);
			}
			if (t.value in scope) return scope[t.value];
			throw new ColorParseError(`unknown keyword "${t.value}" in channel expression: ${text}`);
		}
		throw new ColorParseError(`unexpected "${t.value}" in channel expression: ${text}`);
	};

	const value = parseSum();
	if (pos !== tokens.length) throw new ColorParseError(`trailing input in channel expression: ${text}`);
	return value;
}
