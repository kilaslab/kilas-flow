package jsrun_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/scrypt"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// Expected values come from the documents that define each algorithm (the
// RFCs, FIPS 180 and NIST SP 800-38A/D) or from Go's own crypto packages,
// never from running Node. Node's error names, codes and messages are its
// documented behaviour.

// cryptoPrelude puts crypto and a few helpers in scope. Output is a Buffer
// once the runtime has one and a Uint8Array before that; the helpers read
// either.
var cryptoPrelude = []string{
	"const crypto = require('crypto')",
	"const hexOf = view => Array.from(new Uint8Array(view.buffer, view.byteOffset, view.byteLength), b => b.toString(16).padStart(2, '0')).join('')",
	"const bytes = text => { const out = new Uint8Array(text.length / 2); for (let i = 0; i < out.length; i++) out[i] = parseInt(text.substr(2 * i, 2), 16); return out }",
	"const concat = parts => { const out = new Uint8Array(parts.reduce((n, part) => n + part.length, 0)); let at = 0; for (const part of parts) { out.set(part, at); at += part.length } return out }",
	"const caught = run => { try { run(); return 'no error' } catch (error) { return [error.name, error.code, error.message].join(' | ') } }",
}

// cryptoResult runs a body with the prelude and returns its one item's json.
func cryptoResult(t *testing.T, lines ...string) map[string]any {
	t.Helper()
	source := strings.Join(append(slices.Clone(cryptoPrelude), lines...), "\n")
	return mustRun(t, newRunner(), jsrun.Task{Source: source}).Items[0].JSON
}

// expectFields compares each wanted field by its printed form, so a list
// from JSON compares equal to a list of strings.
func expectFields(t *testing.T, got, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if fmt.Sprint(got[key]) != fmt.Sprint(value) {
			t.Errorf("%s = %v\n\twant %v", key, got[key], value)
		}
	}
}

func hexSum(h hash.Hash, data []byte) string {
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func fromHex(t *testing.T, text string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(text)
	if err != nil {
		t.Fatalf("hex %q: %v", text, err)
	}
	return decoded
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

var cryptoDigests = []struct {
	name string
	new  func() hash.Hash
}{{"md5", md5.New}, {"sha1", sha1.New}, {"sha256", sha256.New}, {"sha384", sha512.New384}, {"sha512", sha512.New}}

func TestCryptoHashesMatchKnownVectors(t *testing.T) {
	abc := []byte("abc")
	var plain []string
	for _, digest := range cryptoDigests {
		plain = append(plain, hexSum(digest.new(), abc))
	}
	sha256abc := hexSum(sha256.New(), abc)
	sum256 := sha256.Sum256(abc)
	got := cryptoResult(t,
		"const names = ['md5', 'sha1', 'sha256', 'sha384', 'sha512']",
		"const first = crypto.createHash('sha256').update('ab')",
		"const second = first.copy()",
		"const raw = crypto.createHash('md5').update('abc').digest()",
		"return [{ json: {",
		"  plain: names.map(name => crypto.createHash(name).update('abc').digest('hex')),",
		"  pieces: names.map(name => crypto.createHash(name).update('a').update('b').update('c').digest('hex')),",
		"  aliases: ['SHA256', 'sha-256', 'SHA2-256', 'RSA-SHA256', 'sha256WithRSAEncryption'].map(name => crypto.createHash(name).update('abc').digest('hex')),",
		"  inputs: [",
		"    crypto.createHash('sha256').update('616263', 'hex').digest('hex'),",
		"    crypto.createHash('sha256').update('YWJj', 'base64').digest('hex'),",
		"    crypto.createHash('sha256').update('abc', 'no-such-encoding').digest('hex'),",
		"    crypto.createHash('sha256').update(new Uint8Array([97, 98, 99])).digest('hex'),",
		"    crypto.createHash('sha256').update(new DataView(new Uint8Array([97, 98, 99]).buffer)).digest('hex'),",
		"    crypto.createHash('sha256').update(new Uint8Array([0, 97, 98, 99, 0]).subarray(1, 4)).digest('hex'),",
		"  ],",
		"  outputs: ['base64', 'base64url', 'HEX'].map(encoding => crypto.createHash('sha256').update('abc').digest(encoding)),",
		"  copies: [first.update('c').digest('hex'), second.update('x').digest('hex')],",
		"  long: crypto.createHash('sha512').update('x'.repeat(1000)).digest('hex'),",
		"  raw: hexOf(raw), rawIsBytes: raw instanceof Uint8Array,",
		"  oneShot: crypto.hash('sha1', 'abc'),",
		"  listed: crypto.getHashes().includes('sha256') && crypto.getHashes().includes('RSA-SHA512'),",
		"  twice: caught(() => { const h = crypto.createHash('md5'); h.digest(); h.digest() }),",
		"  after: caught(() => { const h = crypto.createHash('md5'); h.digest(); h.update('x') }),",
		"  unknown: caught(() => crypto.createHash('foo')),",
		"  number: caught(() => crypto.createHash('md5').update(5)),",
		"  arrayBuffer: caught(() => crypto.createHash('md5').update(new ArrayBuffer(2))),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		// FIPS 180's example for SHA-256, and RFC 1321's for MD5.
		"plain":       plain,
		"pieces":      plain,
		"aliases":     slices.Repeat([]string{sha256abc}, 5),
		"inputs":      slices.Repeat([]string{sha256abc}, 6),
		"outputs":     []string{base64.StdEncoding.EncodeToString(sum256[:]), base64.RawURLEncoding.EncodeToString(sum256[:]), sha256abc},
		"copies":      []string{sha256abc, hexSum(sha256.New(), []byte("abx"))},
		"long":        hexSum(sha512.New(), []byte(strings.Repeat("x", 1000))),
		"raw":         "900150983cd24fb0d6963f7d28e17f72",
		"rawIsBytes":  true,
		"oneShot":     hexSum(sha1.New(), abc),
		"listed":      true,
		"twice":       "Error | ERR_CRYPTO_HASH_FINALIZED | Digest already called",
		"after":       "Error | ERR_CRYPTO_HASH_FINALIZED | Digest already called",
		"unknown":     "Error |  | Digest method not supported",
		"number":      `TypeError | ERR_INVALID_ARG_TYPE | The "data" argument must be of type string or an instance of Buffer, TypedArray, or DataView. Received type number (5)`,
		"arrayBuffer": `TypeError | ERR_INVALID_ARG_TYPE | The "data" argument must be of type string or an instance of Buffer, TypedArray, or DataView. Received an instance of ArrayBuffer`,
	})
	if sha256abc != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("Go's SHA-256 of abc = %s, which is not FIPS 180's", sha256abc)
	}
}

func TestCryptoHmacsMatchRFC4231(t *testing.T) {
	// RFC 4231 test cases 1, 2 and 6; the last key is longer than a block.
	cases := [][2]string{
		{strings.Repeat("0b", 20), "Hi There"},
		{hex.EncodeToString([]byte("Jefe")), "what do ya want for nothing?"},
		{strings.Repeat("aa", 131), "Test Using Larger Than Block-Size Key - Hash Key First"},
	}
	var want [][]string
	for _, digest := range cryptoDigests {
		var macs []string
		for _, check := range cases {
			mac := hmac.New(digest.new, fromHex(t, check[0]))
			mac.Write([]byte(check[1]))
			macs = append(macs, hex.EncodeToString(mac.Sum(nil)))
		}
		want = append(want, macs)
	}
	rfc4231 := []string{
		"b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
		"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
		"60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54",
	}
	if !slices.Equal(want[2], rfc4231) {
		t.Fatalf("Go's HMAC-SHA-256 = %v, which is not RFC 4231's %v", want[2], rfc4231)
	}
	got := cryptoResult(t,
		"const cases = "+jsonOf(t, cases),
		"const names = ['md5', 'sha1', 'sha256', 'sha384', 'sha512']",
		"return [{ json: {",
		"  macs: names.map(name => cases.map(([key, data]) => crypto.createHmac(name, bytes(key)).update(data).digest('hex'))),",
		"  stringKey: crypto.createHmac('SHA256', 'Jefe').update('what do ya ').update('want for nothing?').digest('hex'),",
		"  hexKey: crypto.createHmac('sha256', '4a656665', { encoding: 'hex' }).update('what do ya want for nothing?').digest('hex'),",
		"  second: (() => { const mac = crypto.createHmac('sha1', 'k'); mac.digest(); return mac.digest('hex') })(),",
		"  after: caught(() => { const mac = crypto.createHmac('sha1', 'k'); mac.digest(); mac.update('x') }),",
		"  unknown: caught(() => crypto.createHmac('foo', 'k')),",
		"  noKey: caught(() => crypto.createHmac('sha256', null)),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		"macs":      want,
		"stringKey": rfc4231[1],
		"hexKey":    rfc4231[1],
		"second":    "",
		"after":     "Error | ERR_CRYPTO_HASH_FINALIZED | Digest already called",
		"unknown":   "TypeError | ERR_CRYPTO_INVALID_DIGEST | Invalid digest: foo",
		"noKey":     `TypeError | ERR_INVALID_ARG_TYPE | The "key" argument must be of type string or an instance of ArrayBuffer, Buffer, TypedArray, DataView, KeyObject, or CryptoKey. Received null`,
	})
}

func TestCryptoPBKDF2MatchesRFC6070(t *testing.T) {
	var longer []string
	for _, digest := range cryptoDigests {
		if digest.name == "sha1" {
			continue
		}
		key, err := pbkdf2.Key(digest.new, "password", []byte("salt"), 1000, 100)
		if err != nil {
			t.Fatal(err)
		}
		longer = append(longer, hex.EncodeToString(key))
	}
	got := cryptoResult(t,
		"const derive = (password, salt, iterations, length, digest) => hexOf(crypto.pbkdf2Sync(password, salt, iterations, length, digest))",
		"const promised = hexOf(await require('util').promisify(crypto.pbkdf2)('password', 'salt', 2, 20, 'sha1'))",
		"const called = await new Promise((resolve, reject) => crypto.pbkdf2('password', 'salt', 1, 20, 'SHA1', (error, key) => error ? reject(error) : resolve(hexOf(key))))",
		"return [{ json: {",
		"  rfc6070: [",
		"    derive('password', 'salt', 1, 20, 'sha1'),",
		"    derive('password', 'salt', 2, 20, 'sha1'),",
		"    derive('password', 'salt', 4096, 20, 'sha1'),",
		"    derive('passwordPASSWORDpassword', 'saltSALTsaltSALTsaltSALTsaltSALTsalt', 4096, 25, 'sha1'),",
		"    derive('pass\\u0000word', new Uint8Array([115, 97, 0, 108, 116]), 4096, 16, 'sha1'),",
		"  ],",
		"  longer: ['md5', 'sha256', 'sha384', 'sha512'].map(digest => derive('password', 'salt', 1000, 100, digest)),",
		"  promised, called,",
		"  unknown: caught(() => crypto.pbkdf2Sync('p', 's', 1, 10, 'foo')),",
		"  noDigest: caught(() => crypto.pbkdf2Sync('p', 's', 1, 10)),",
		"  noIterations: caught(() => crypto.pbkdf2Sync('p', 's', 0, 10, 'sha1')),",
		"  fraction: caught(() => crypto.pbkdf2Sync('p', 's', 1.5, 10, 'sha1')),",
		"  nothing: caught(() => crypto.pbkdf2Sync('p', 's', 1, 0, 'sha1')),",
		"  noCallback: caught(() => crypto.pbkdf2('p', 's', 1, 10, 'sha1')),",
		"  callbackForDigest: caught(() => crypto.pbkdf2('p', 's', 1, 10, () => {})),",
		"  nothingLater: await new Promise(resolve => crypto.pbkdf2('p', 's', 1, 0, 'sha1', (error, key) => resolve(error.message + ', ' + key))),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		"rfc6070": []string{
			"0c60c80f961f0e71f3a9b524af6012062fe037a6",
			"ea6c014dc72d6f8ccd1ed92ace1d41f0d8de8957",
			"4b007901b765489abead49d926f721d065a429c1",
			"3d2eec4fe41c849b80c8d83662c0e44a8b291a964cf2f07038",
			"56fa6aa75548099dcc37d7f03425e0c3",
		},
		"longer":       longer,
		"promised":     "ea6c014dc72d6f8ccd1ed92ace1d41f0d8de8957",
		"called":       "0c60c80f961f0e71f3a9b524af6012062fe037a6",
		"unknown":      "TypeError | ERR_CRYPTO_INVALID_DIGEST | Invalid digest: foo",
		"noDigest":     `TypeError | ERR_INVALID_ARG_TYPE | The "digest" argument must be of type string. Received undefined`,
		"noIterations": `RangeError | ERR_OUT_OF_RANGE | The value of "iterations" is out of range. It must be >= 1 && <= 2147483647. Received 0`,
		"fraction":     `RangeError | ERR_OUT_OF_RANGE | The value of "iterations" is out of range. It must be an integer. Received 1.5`,
		"nothing":      "Error |  | Deriving bits failed",
		"noCallback":   `TypeError | ERR_INVALID_ARG_TYPE | The "callback" argument must be of type function. Received undefined`,
		// A callback in the digest's place leaves the digest missing.
		"callbackForDigest": `TypeError | ERR_INVALID_ARG_TYPE | The "digest" argument must be of type string. Received undefined`,
		"nothingLater":      "Deriving bits failed, undefined",
	})
}

func TestCryptoScryptMatchesRFC7914(t *testing.T) {
	rfc7914 := []string{
		"77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906",
		"fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b3731622eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640",
	}
	derive := func(password, salt string, n, r, p, length int) string {
		key, err := scrypt.Key([]byte(password), []byte(salt), n, r, p, length)
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(key)
	}
	if derive("", "", 16, 1, 1, 64) != rfc7914[0] || derive("password", "NaCl", 1024, 8, 16, 64) != rfc7914[1] {
		t.Fatal("Go's scrypt disagrees with RFC 7914")
	}
	got := cryptoResult(t,
		"const promised = hexOf(await require('util').promisify(crypto.scrypt)('secret', 'salt', 16, { N: 1024 }))",
		"const called = await new Promise(resolve => crypto.scrypt('secret', 'salt', 16, (error, key) => resolve(error ? String(error) : hexOf(key))))",
		"return [{ json: {",
		"  rfc7914: [",
		"    hexOf(crypto.scryptSync('', '', 64, { N: 16, r: 1, p: 1 })),",
		"    hexOf(crypto.scryptSync('password', 'NaCl', 64, { cost: 1024, blockSize: 8, parallelization: 16 })),",
		"  ],",
		"  defaults: hexOf(crypto.scryptSync('secret', 'salt', 32)),",
		"  zeroMeansDefault: hexOf(crypto.scryptSync('secret', 'salt', 32, { N: 0, r: 0, p: 0, maxmem: 0 })),",
		"  promised, called,",
		"  empty: crypto.scryptSync('p', 's', 0).length,",
		"  notPowerOfTwo: caught(() => crypto.scryptSync('p', 's', 16, { N: 1000 })),",
		"  maxmem: caught(() => crypto.scryptSync('p', 's', 16, { N: 16384, maxmem: 16 * 1024 * 1024 })),",
		"  smallR: caught(() => crypto.scryptSync('p', 's', 16, { N: 65536, r: 1, maxmem: 2 ** 40 })),",
		"  pair: caught(() => crypto.scryptSync('p', 's', 16, { N: 16, cost: 16 })),",
		"  wrongType: caught(() => crypto.scryptSync('p', 's', 16, { r: '8' })),",
		"} }]",
	)
	defaults := derive("secret", "salt", 16384, 8, 1, 32)
	memory := "RangeError | ERR_CRYPTO_INVALID_SCRYPT_PARAMS | Invalid scrypt params: error:030000AC:digital envelope routines::memory limit exceeded"
	expectFields(t, got, map[string]any{
		"rfc7914":          rfc7914,
		"defaults":         defaults,
		"zeroMeansDefault": defaults,
		"promised":         derive("secret", "salt", 1024, 8, 1, 16),
		"called":           derive("secret", "salt", 16384, 8, 1, 16),
		"empty":            0,
		"notPowerOfTwo":    "RangeError | ERR_CRYPTO_INVALID_SCRYPT_PARAMS | Invalid scrypt params",
		"maxmem":           memory,
		"smallR":           memory,
		"pair":             `TypeError | ERR_INCOMPATIBLE_OPTION_PAIR | Option "N" cannot be used in combination with option "cost"`,
		"wrongType":        `TypeError | ERR_INVALID_ARG_TYPE | The "r" argument must be of type number. Received type string ('8')`,
	})
}

// goCipher is the reference each AES mode is checked against: Go's own.
func goCipher(t *testing.T, mode string, key, iv, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "cbc":
		padding := aes.BlockSize - len(plaintext)%aes.BlockSize
		padded := append(slices.Clone(plaintext), bytes.Repeat([]byte{byte(padding)}, padding)...)
		out := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
		return out
	case "ctr":
		out := make([]byte, len(plaintext))
		cipher.NewCTR(block, iv).XORKeyStream(out, plaintext)
		return out
	}
	aead, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		t.Fatal(err)
	}
	return aead.Seal(nil, iv, plaintext, nil)
}

func TestCryptoAESMatchesNISTVectors(t *testing.T) {
	// NIST SP 800-38A, F.2.1 (CBC-AES128.Encrypt) and F.5.1 (CTR-AES128.Encrypt).
	key := "2b7e151628aed2a6abf7158809cf4f3c"
	plaintext := "6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411e5fbc1191a0a52eff69f2445df4f9b17ad2b417be66c3710"
	cbcIV, ctrIV := "000102030405060708090a0b0c0d0e0f", "f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff"
	cbcWant := "7649abac8119b246cee98e9b12e9197d5086cb9b507219ee95db113a917678b273bed6b8e3c1743b7116e69e222295163ff1caa1681fac09120eca307586e1a7"
	ctrWant := "874d6191b620e3261bef6864990db6ce9806f66b7970fdff8617187bb9fffdff5ae4df3edbd5d35e5b4f09020db03eab1e031dda2fbe03d1792170a0f3009cee"
	if got := hex.EncodeToString(goCipher(t, "ctr", fromHex(t, key), fromHex(t, ctrIV), fromHex(t, plaintext))); got != ctrWant {
		t.Fatalf("Go's CTR = %s, which is not SP 800-38A's", got)
	}

	// Every key size and mode, against Go, on a message that is not whole
	// blocks, and one that crosses a carry in CTR's 128-bit counter.
	type check struct{ Name, Key, IV, Plaintext, Want string }
	var checks []check
	message := []byte(strings.Repeat("KilasFlow runs this in Go. ", 7))
	for _, mode := range []string{"cbc", "ctr", "gcm"} {
		for _, size := range []int{128, 192, 256} {
			key := bytes.Repeat([]byte{byte(size / 8)}, size/8)
			iv := bytes.Repeat([]byte{0x42}, 16)
			if mode == "gcm" {
				iv = iv[:12]
			}
			if mode == "ctr" {
				iv = fromHex(t, "0102030405060708fffffffffffffffe")
			}
			checks = append(checks, check{
				Name: fmt.Sprintf("aes-%d-%s", size, mode), Key: hex.EncodeToString(key), IV: hex.EncodeToString(iv),
				Plaintext: hex.EncodeToString(message), Want: hex.EncodeToString(goCipher(t, mode, key, iv, message)),
			})
		}
	}
	got := cryptoResult(t,
		"const checks = "+jsonOf(t, checks),
		"const run = (name, key, iv, plaintext, autoPadding) => {",
		"  const cipher = crypto.createCipheriv(name, bytes(key), bytes(iv))",
		"  if (autoPadding === false) cipher.setAutoPadding(false)",
		"  let out = ''",
		"  for (let at = 0; at < plaintext.length; at += 26) out += cipher.update(plaintext.slice(at, at + 26), 'hex', 'hex')",
		"  out += cipher.final('hex')",
		"  return name.endsWith('gcm') ? out + hexOf(cipher.getAuthTag()) : out",
		"}",
		"return [{ json: {",
		"  cbc: run('aes-128-cbc', '"+key+"', '"+cbcIV+"', '"+plaintext+"', false),",
		"  ctr: run('AES-128-CTR', '"+key+"', '"+ctrIV+"', '"+plaintext+"'),",
		"  all: checks.map(check => run(check.Name, check.Key, check.IV, check.Plaintext) === check.Want),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		"cbc": cbcWant,
		"ctr": ctrWant,
		"all": slices.Repeat([]bool{true}, len(checks)),
	})
}

func TestCryptoGCMMatchesTheSpecAndRefusesForgeries(t *testing.T) {
	// The GCM specification's test cases 2 and 3 (McGrew and Viega, as NIST
	// SP 800-38D's validation uses them), and 4 and 6: additional data, and a
	// 60-byte IV, which GCM hashes into its first counter block.
	key := "feffe9928665731c6d6a8f9467308308"
	plaintext := "d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39"
	aad := "feedfacedeadbeeffeedfacedeadbeefabaddad2"
	longIV := "9313225df88406e555909c5aff5269aa6a7a9538534f7da1e4c303d2a318a728c3c0c95156809539fcf0e2429a6b525416aedbf5a0de6a57a637b39b"
	seal := func(keyHex, ivHex, plainHex, aadHex string) string {
		block, _ := aes.NewCipher(fromHex(t, keyHex))
		aead, err := cipher.NewGCMWithNonceSize(block, len(ivHex)/2)
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(aead.Seal(nil, fromHex(t, ivHex), fromHex(t, plainHex), fromHex(t, aadHex)))
	}
	case2 := "0388dace60b6a392f328c2b971b2fe78" + "ab6e47d42cec13bdf53a67b21257bddf"
	case3 := "42831ec2217774244b7221b784d0d49ce3aa212f2c02a4e035c17e2329aca12e21d514b25466931c7d8f6a5aac84aa051ba30b396a0aac973d58e091473f5985" + "4d5c2af327cd64a62cf35abd2ba6fab4"
	if seal(strings.Repeat("00", 16), strings.Repeat("00", 12), strings.Repeat("00", 16), "") != case2 ||
		seal(key, "cafebabefacedbaddecaf888", plaintext+"1aafd255", "") != case3 {
		t.Fatal("Go's GCM disagrees with the specification's test cases")
	}
	case4 := seal(key, "cafebabefacedbaddecaf888", plaintext, aad)
	case6 := seal(key, longIV, plaintext, aad)
	got := cryptoResult(t,
		"const seal = (key, iv, plaintext, aad, options) => {",
		"  const cipher = crypto.createCipheriv('aes-' + key.length * 4 + '-gcm', bytes(key), bytes(iv), options)",
		"  if (aad) { cipher.setAAD(bytes(aad.slice(0, 8))); cipher.setAAD(bytes(aad.slice(8))) }",
		"  const out = cipher.update(plaintext, 'hex', 'hex') + cipher.final('hex')",
		"  return out + hexOf(cipher.getAuthTag())",
		"}",
		"const open = (key, iv, sealed, aad, tagLength, options) => {",
		"  const decipher = crypto.createDecipheriv('aes-' + key.length * 4 + '-gcm', bytes(key), bytes(iv), options)",
		"  if (aad) decipher.setAAD(bytes(aad))",
		"  const tagAt = sealed.length - 32",
		"  const early = decipher.update(sealed.slice(0, tagAt), 'hex', 'hex')",
		"  decipher.setAuthTag(bytes(sealed.slice(tagAt, tagAt + 2 * (tagLength || 16))))",
		"  return early + decipher.final('hex')",
		"}",
		"const flip = (text, at) => text.slice(0, at) + (text[at] === '0' ? '1' : '0') + text.slice(at + 1)",
		"const c4 = seal('"+key+"', 'cafebabefacedbaddecaf888', '"+plaintext+"', '"+aad+"')",
		"return [{ json: {",
		"  case2: seal('00'.repeat(16), '00'.repeat(12), '00'.repeat(16)),",
		"  case3: seal('"+key+"', 'cafebabefacedbaddecaf888', '"+plaintext+"1aafd255'),",
		"  case4: c4,",
		"  case6: seal('"+key+"', '"+longIV+"', '"+plaintext+"', '"+aad+"'),",
		"  opened: open('"+key+"', 'cafebabefacedbaddecaf888', c4, '"+aad+"'),",
		"  openedLongIV: open('"+key+"', '"+longIV+"', '"+case6+"', '"+aad+"'),",
		"  short: seal('"+key+"', 'cafebabefacedbaddecaf888', '"+plaintext+"', '"+aad+"', { authTagLength: 8 }),",
		"  openedShort: [12, 8, 4].map(length => open('"+key+"', 'cafebabefacedbaddecaf888', c4, '"+aad+"', length, length === 12 ? undefined : { authTagLength: length })),",
		"  forgedText: caught(() => open('"+key+"', 'cafebabefacedbaddecaf888', flip(c4, 3), '"+aad+"')),",
		"  forgedTag: caught(() => open('"+key+"', 'cafebabefacedbaddecaf888', flip(c4, c4.length - 1), '"+aad+"')),",
		"  forgedShortTag: caught(() => open('"+key+"', 'cafebabefacedbaddecaf888', flip(c4, c4.length - 32), '"+aad+"', 12)),",
		"  forgedAAD: caught(() => open('"+key+"', 'cafebabefacedbaddecaf888', c4, flip('"+aad+"', 0))),",
		"  wrongTagLength: caught(() => open('"+key+"', 'cafebabefacedbaddecaf888', c4, '"+aad+"', 16, { authTagLength: 12 })),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		"case2":          case2,
		"case3":          case3,
		"case4":          case4,
		"case6":          case6,
		"opened":         plaintext,
		"openedLongIV":   plaintext,
		"short":          case4[:len(plaintext)] + case4[len(plaintext):len(plaintext)+16],
		"openedShort":    []string{plaintext, plaintext, plaintext},
		"forgedText":     "Error |  | Unsupported state or unable to authenticate data",
		"forgedTag":      "Error |  | Unsupported state or unable to authenticate data",
		"forgedShortTag": "Error |  | Unsupported state or unable to authenticate data",
		"forgedAAD":      "Error |  | Unsupported state or unable to authenticate data",
		"wrongTagLength": "TypeError | ERR_CRYPTO_INVALID_AUTH_TAG | Invalid authentication tag length: 16",
	})
}

// GCM counts blocks in the low 32 bits of its counter and wraps there (NIST
// SP 800-38D's inc32), where CTR mode carries into the rest. With a 16-byte
// IV the first counter block is a hash of the IV, so its low bits can be
// anywhere; this IV's are 25,949 blocks short of wrapping, which the message
// crosses part way through an update.
func TestCryptoGCMStreamsAcrossItsCounterWrap(t *testing.T) {
	key := fromHex(t, "000102030405060708090a0b0c0d0e0f")
	iv := fromHex(t, "0000000000000000000000000001d29b")
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCMWithNonceSize(block, len(iv))
	var zero, counter [16]byte
	block.Decrypt(counter[:], aead.Seal(nil, iv, zero[:], nil)[:16])
	if left := math.MaxUint32 - uint64(binary.BigEndian.Uint32(counter[12:])) + 1; left != 25949 {
		t.Fatalf("the IV's counter wraps after %d blocks, want 25949", left)
	}
	pattern := make([]byte, 4096)
	for index := range pattern {
		pattern[index] = byte(index*31 + 7)
	}
	message := bytes.Repeat(pattern, 256)
	sealed := aead.Seal(nil, iv, message, nil)
	cipherSum := sha256.Sum256(sealed[:len(message)])
	plainSum := sha256.Sum256(message)

	got := cryptoResult(t,
		fmt.Sprintf("const key = bytes('%x'), iv = bytes('%x')", key, iv),
		"const pattern = new Uint8Array(4096)",
		"for (let i = 0; i < pattern.length; i++) pattern[i] = (i * 31 + 7) & 255",
		"const message = new Uint8Array(1 << 20)",
		"for (let at = 0; at < message.length; at += pattern.length) message.set(pattern, at)",
		"const cipher = crypto.createCipheriv('aes-128-gcm', key, iv)",
		"const sealed = []",
		"let at = 0",
		"for (const size of [1000, 333333, 1 << 20]) { const end = Math.min(at + size, message.length); sealed.push(cipher.update(message.subarray(at, end))); at = end }",
		"sealed.push(cipher.final())",
		"const tag = cipher.getAuthTag()",
		"const decipher = crypto.createDecipheriv('aes-128-gcm', key, iv)",
		"const cipherSum = crypto.createHash('sha256'), plainSum = crypto.createHash('sha256')",
		"for (const piece of sealed) { cipherSum.update(piece); plainSum.update(decipher.update(piece)) }",
		"decipher.setAuthTag(tag)",
		"plainSum.update(decipher.final())",
		"return [{ json: { sizes: sealed.map(piece => piece.length), cipher: cipherSum.digest('hex'), tag: hexOf(tag), plain: plainSum.digest('hex') } }]",
	)
	expectFields(t, got, map[string]any{
		"sizes":  []int{1000, 333333, 1<<20 - 334333, 0},
		"cipher": hex.EncodeToString(cipherSum[:]),
		"tag":    hex.EncodeToString(sealed[len(message):]),
		"plain":  hex.EncodeToString(plainSum[:]),
	})
}

// Node returns cipher output as it goes, and carries a partial character or
// base64 group into the next call, so the pieces joined are the whole.
func TestCryptoCipherOutputStreamsLikeNode(t *testing.T) {
	key, iv := bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)
	whole := base64.StdEncoding.EncodeToString(goCipher(t, "cbc", key, iv, []byte(strings.Repeat("a", 20)+strings.Repeat("b", 20))))
	got := cryptoResult(t,
		"const key = new Uint8Array(16).fill(1), iv = new Uint8Array(16).fill(2)",
		"const blocks = crypto.createCipheriv('aes-128-cbc', key, iv)",
		"const encryptSizes = [blocks.update('x'.repeat(15)).length, blocks.update('x').length, blocks.update('x'.repeat(16)).length, blocks.final().length]",
		"const whole = crypto.createCipheriv('aes-128-cbc', key, iv)",
		"const sealed = concat([whole.update('x'.repeat(32)), whole.final()])",
		"const unblocks = crypto.createDecipheriv('aes-128-cbc', key, iv)",
		"const decryptSizes = [unblocks.update(sealed.subarray(0, 32)).length, unblocks.update(sealed.subarray(32)).length, unblocks.final().length]",
		"const b64 = crypto.createCipheriv('aes-128-cbc', key, iv)",
		"const base64 = [b64.update('a'.repeat(20), 'utf8', 'base64'), b64.update('b'.repeat(20), 'utf8', 'base64'), b64.final('base64')]",
		"const text = 'é€𝄞x'",
		"const utf8 = []",
		"{ const sealed = crypto.createCipheriv('aes-128-ctr', key, iv).update(text); const decipher = crypto.createDecipheriv('aes-128-ctr', key, iv)",
		"  for (let i = 0; i < sealed.length; i++) utf8.push(decipher.update(sealed.subarray(i, i + 1), undefined, 'utf8'))",
		"  utf8.push(decipher.final('utf8')) }",
		"const utf16 = []",
		"{ const sealed = crypto.createCipheriv('aes-256-ctr', new Uint8Array(32), iv).update('h𝄞', 'utf16le'); const decipher = crypto.createDecipheriv('aes-256-ctr', new Uint8Array(32), iv)",
		"  for (let i = 0; i < sealed.length; i++) utf16.push(decipher.update(sealed.subarray(i, i + 1), undefined, 'utf16le'))",
		"  utf16.push(decipher.final('utf16le')) }",
		"const unpadded = crypto.createCipheriv('aes-192-cbc', new Uint8Array(24), iv).setAutoPadding(false)",
		"const raw = concat([unpadded.update('y'.repeat(20)), unpadded.update('y'.repeat(12)), unpadded.final()])",
		"const unpad = crypto.createDecipheriv('aes-192-cbc', new Uint8Array(24), iv).setAutoPadding(false)",
		"const roundTrips = ['cbc', 'ctr', 'gcm'].flatMap(mode => [128, 192, 256].map(size => 'aes-' + size + '-' + mode)).map(name => {",
		"  const key = crypto.randomBytes(Number(name.slice(4, 7)) / 8), iv = crypto.randomBytes(name.endsWith('gcm') ? 12 : 16)",
		"  const text = 'Kilas ' + 'ƒlow '.repeat(50)",
		"  const cipher = crypto.createCipheriv(name, key, iv)",
		"  let sealed = ''",
		"  for (const piece of text.match(/.{1,7}/g)) sealed += cipher.update(piece, 'utf8', 'base64')",
		"  sealed += cipher.final('base64')",
		"  const decipher = crypto.createDecipheriv(name, key, iv)",
		"  if (name.endsWith('gcm')) decipher.setAuthTag(cipher.getAuthTag())",
		"  let opened = ''",
		"  for (const piece of sealed.match(/.{1,8}/g)) opened += decipher.update(piece, 'base64', 'utf8')",
		"  return opened + decipher.final('utf8') === text",
		"})",
		"return [{ json: {",
		"  encryptSizes, decryptSizes,",
		"  base64Sizes: base64.map(piece => piece.length), base64: base64.join(''),",
		"  utf8, utf16,",
		"  unpadded: raw.length, unpaddedBack: unpad.update(raw, undefined, 'utf8') + unpad.final('utf8'),",
		"  roundTrips,",
		"  changed: caught(() => { const c = crypto.createCipheriv('aes-128-ctr', key, iv); c.update('a', 'utf8', 'hex'); c.update('a', 'utf8', 'base64') }),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		"encryptSizes": []int{0, 16, 16, 16},
		"decryptSizes": []int{16, 16, 0},
		"base64Sizes":  []int{20, 20, 24},
		"base64":       whole,
		"utf8":         []string{"", "é", "", "", "€", "", "", "", "𝄞", "x", ""},
		// Node hands back the high surrogate on its own; a string that
		// passes through Go cannot hold one, so here it waits for its pair.
		// Joined, the pieces are the same.
		"utf16":        []string{"", "h", "", "", "", "𝄞", ""},
		"unpadded":     32,
		"unpaddedBack": strings.Repeat("y", 32),
		"roundTrips":   slices.Repeat([]bool{true}, 9),
		"changed":      "Error |  | Cannot change encoding",
	})
}

func TestCryptoCipherErrorsAreNodes(t *testing.T) {
	got := cryptoResult(t,
		"const key = new Uint8Array(16), iv = new Uint8Array(16), nonce = new Uint8Array(12)",
		"return [{ json: {",
		"  unknownCipher: caught(() => crypto.createCipheriv('aes-128-xyz', key, iv)),",
		"  keyLength: caught(() => crypto.createCipheriv('aes-256-cbc', key, iv)),",
		"  ivLength: caught(() => crypto.createCipheriv('aes-128-cbc', key, nonce)),",
		"  longGCMIV: caught(() => crypto.createCipheriv('aes-128-gcm', key, new Uint8Array(129))),",
		"  nullIV: caught(() => crypto.createCipheriv('aes-128-ctr', key, null)),",
		"  missingIV: caught(() => crypto.createCipheriv('aes-128-ctr', key)),",
		"  cipherName: caught(() => crypto.createCipheriv(5, key, iv)),",
		"  badDecrypt: caught(() => { const d = crypto.createDecipheriv('aes-128-cbc', key, iv); d.update(new Uint8Array(16)); d.final() }),",
		"  wrongLength: caught(() => { const d = crypto.createDecipheriv('aes-128-cbc', key, iv); d.update(new Uint8Array(15)); d.final() }),",
		"  unpadded: caught(() => { const c = crypto.createCipheriv('aes-128-cbc', key, iv); c.setAutoPadding(false); c.update('abc'); c.final() }),",
		"  noArgumentMeansNoPadding: caught(() => { const c = crypto.createCipheriv('aes-128-cbc', key, iv); c.setAutoPadding(); c.update('abc'); c.final() }),",
		"  afterFinal: caught(() => { const c = crypto.createCipheriv('aes-128-cbc', key, iv); c.final(); c.update('x') }),",
		"  finalTwice: caught(() => { const c = crypto.createCipheriv('aes-128-gcm', key, nonce); c.final(); c.final() }),",
		"  paddingAfterFinal: caught(() => { const c = crypto.createCipheriv('aes-128-cbc', key, iv); c.final(); c.setAutoPadding(false) }),",
		"  aadLate: caught(() => { const c = crypto.createCipheriv('aes-128-gcm', key, nonce); c.update('x'); c.setAAD(new Uint8Array(1)) }),",
		"  aadOnCBC: caught(() => crypto.createCipheriv('aes-128-cbc', key, iv).setAAD(new Uint8Array(1))),",
		"  tagEarly: caught(() => crypto.createCipheriv('aes-128-gcm', key, nonce).getAuthTag()),",
		"  tagOnCTR: caught(() => { const c = crypto.createCipheriv('aes-128-ctr', key, iv); c.final(); c.getAuthTag() }),",
		"  tagLength: caught(() => crypto.createDecipheriv('aes-128-gcm', key, nonce).setAuthTag(new Uint8Array(3))),",
		"  tagOption: caught(() => crypto.createCipheriv('aes-128-gcm', key, nonce, { authTagLength: 7 })),",
		"  tagTwice: caught(() => crypto.createDecipheriv('aes-128-gcm', key, nonce).setAuthTag(new Uint8Array(16)).setAuthTag(new Uint8Array(16))),",
		"  tagMissing: caught(() => { const d = crypto.createDecipheriv('aes-128-gcm', key, nonce); d.update(new Uint8Array(5)); d.final() }),",
		"  noSetAuthTagOnCipher: typeof crypto.createCipheriv('aes-128-gcm', key, nonce).setAuthTag,",
		"  outputEncoding: caught(() => crypto.createCipheriv('aes-128-ctr', key, iv).update('x', 'utf8', 'klingon')),",
		"  data: caught(() => crypto.createCipheriv('aes-128-ctr', key, iv).update(5)),",
		"  names: [crypto.createCipheriv('aes128', key, iv).constructor.name, crypto.createDecipheriv('ID-AES128-GCM', key, nonce).constructor.name],",
		"} }]",
	)
	state := "Error | ERR_CRYPTO_INVALID_STATE | Invalid state for operation "
	unauthenticated := "Error |  | Unsupported state or unable to authenticate data"
	wrongLength := "Error | ERR_OSSL_WRONG_FINAL_BLOCK_LENGTH | error:1C80006B:Provider routines::wrong final block length"
	invalidIV := "TypeError | ERR_CRYPTO_INVALID_IV | Invalid initialization vector"
	expectFields(t, got, map[string]any{
		"unknownCipher":            "Error | ERR_CRYPTO_UNKNOWN_CIPHER | Unknown cipher",
		"keyLength":                "RangeError | ERR_CRYPTO_INVALID_KEYLEN | Invalid key length",
		"ivLength":                 invalidIV,
		"longGCMIV":                invalidIV,
		"nullIV":                   invalidIV,
		"missingIV":                `TypeError | ERR_INVALID_ARG_TYPE | The "iv" argument must be of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView. Received undefined`,
		"cipherName":               `TypeError | ERR_INVALID_ARG_TYPE | The "cipher" argument must be of type string. Received type number (5)`,
		"badDecrypt":               "Error | ERR_OSSL_BAD_DECRYPT | error:1C800064:Provider routines::bad decrypt",
		"wrongLength":              wrongLength,
		"unpadded":                 wrongLength,
		"noArgumentMeansNoPadding": wrongLength,
		"afterFinal":               "Error |  | Trying to add data in unsupported state",
		"finalTwice":               "Error | ERR_CRYPTO_INVALID_STATE | Invalid state",
		"paddingAfterFinal":        state + "setAutoPadding",
		"aadLate":                  state + "setAAD",
		"aadOnCBC":                 state + "setAAD",
		"tagEarly":                 state + "getAuthTag",
		"tagOnCTR":                 state + "getAuthTag",
		"tagLength":                "TypeError | ERR_CRYPTO_INVALID_AUTH_TAG | Invalid authentication tag length: 3",
		"tagOption":                "TypeError | ERR_CRYPTO_INVALID_AUTH_TAG | Invalid authentication tag length: 7",
		"tagTwice":                 state + "setAuthTag",
		"tagMissing":               unauthenticated,
		"noSetAuthTagOnCipher":     "undefined",
		"outputEncoding":           "TypeError | ERR_UNKNOWN_ENCODING | Unknown encoding: klingon",
		"data":                     `TypeError | ERR_INVALID_ARG_TYPE | The "data" argument must be of type string or an instance of Buffer, TypedArray, or DataView. Received type number (5)`,
		"names":                    []string{"Cipheriv", "Decipheriv"},
	})
}

func TestCryptoRandomValuesAreWellFormed(t *testing.T) {
	got := cryptoResult(t,
		"const pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/",
		"const uuids = Array.from({ length: 20 }, () => crypto.randomUUID())",
		"const small = Array.from({ length: 300 }, () => crypto.randomInt(3))",
		"const ranged = Array.from({ length: 300 }, () => crypto.randomInt(-5, -2))",
		"const filled = new Uint32Array(8)",
		"const window = new Uint8Array(32)",
		"crypto.getRandomValues(window.subarray(8, 16))",
		"return [{ json: {",
		"  sizes: [crypto.randomBytes(0).length, crypto.randomBytes(16).length, crypto.randomBytes(1.9).length],",
		"  distinct: hexOf(crypto.randomBytes(16)) !== hexOf(crypto.randomBytes(16)),",
		"  isBytes: crypto.randomBytes(4) instanceof Uint8Array,",
		"  callback: await new Promise(resolve => crypto.randomBytes(8, (error, bytes) => resolve(error === null && bytes.length === 8))),",
		"  uuids: uuids.every(id => pattern.test(id)) && new Set(uuids).size === uuids.length,",
		"  globalUUID: pattern.test(globalThis.crypto.randomUUID()),",
		"  small: [...new Set(small)].sort(),",
		"  ranged: ranged.every(n => n >= -5 && n < -2 && Number.isInteger(n)),",
		"  intCallback: await new Promise(resolve => crypto.randomInt(10, 20, (error, n) => resolve(error === undefined && n >= 10 && n < 20))),",
		"  sameArray: crypto.getRandomValues(filled) === filled && filled.some(value => value !== 0),",
		"  window: window.subarray(0, 8).every(v => v === 0) && window.subarray(16).every(v => v === 0),",
		"  webcrypto: crypto.webcrypto.getRandomValues(new Int8Array(4)).length === 4 && globalThis.crypto.getRandomValues(new Int16Array(4)).length === 4,",
		"  negative: caught(() => crypto.randomBytes(-1)),",
		"  text: caught(() => crypto.randomBytes('5')),",
		"  empty: caught(() => crypto.randomInt(5, 5)),",
		"  fraction: caught(() => crypto.randomInt(1.5)),",
		"  wide: caught(() => crypto.randomInt(0, 2 ** 48)),",
		"  wideMax: caught(() => crypto.randomInt(2 ** 48 + 1)),",
		"  quota: caught(() => crypto.getRandomValues(new Uint8Array(65537))),",
		"  floats: caught(() => crypto.getRandomValues(new Float64Array(2))),",
		"  options: caught(() => crypto.randomUUID(5)),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		"sizes":       []int{0, 16, 1},
		"distinct":    true,
		"isBytes":     true,
		"callback":    true,
		"uuids":       true,
		"globalUUID":  true,
		"small":       []int{0, 1, 2},
		"ranged":      true,
		"intCallback": true,
		"sameArray":   true,
		"window":      true,
		"webcrypto":   true,
		"negative":    `RangeError | ERR_OUT_OF_RANGE | The value of "size" is out of range. It must be >= 0 && <= 2147483647. Received -1`,
		"text":        `TypeError | ERR_INVALID_ARG_TYPE | The "size" argument must be of type number. Received type string ('5')`,
		"empty":       `RangeError | ERR_OUT_OF_RANGE | The value of "max" is out of range. It must be greater than the value of "min" (5). Received 5`,
		"fraction":    `TypeError | ERR_INVALID_ARG_TYPE | The "max" argument must be a safe integer. Received type number (1.5)`,
		"wide":        `RangeError | ERR_OUT_OF_RANGE | The value of "max - min" is out of range. It must be <= 281474976710655. Received 281_474_976_710_656`,
		"wideMax":     `RangeError | ERR_OUT_OF_RANGE | The value of "max" is out of range. It must be <= 281474976710655. Received 281_474_976_710_657`,
		"quota":       "QuotaExceededError | 22 | The requested length exceeds 65,536 bytes",
		"floats":      "TypeMismatchError | 17 | The data argument must be an integer-type TypedArray",
		"options":     `TypeError | ERR_INVALID_ARG_TYPE | The "options" argument must be of type object. Received type number (5)`,
	})
}

func TestCryptoTimingSafeEqualComparesBytes(t *testing.T) {
	got := cryptoResult(t,
		"return [{ json: {",
		"  equal: crypto.timingSafeEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 3])),",
		"  unequal: crypto.timingSafeEqual(new Uint8Array([1, 2, 3]), new Uint8Array([1, 2, 4])),",
		"  views: crypto.timingSafeEqual(new Uint16Array([0x0201]), new Uint8Array([1, 2])),",
		"  buffers: crypto.timingSafeEqual(new ArrayBuffer(4), new DataView(new ArrayBuffer(4))),",
		"  lengths: caught(() => crypto.timingSafeEqual(new Uint8Array(2), new Uint8Array(3))),",
		"  strings: caught(() => crypto.timingSafeEqual('a', 'a')),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		"equal":   true,
		"unequal": false,
		"views":   true,
		"buffers": true,
		"lengths": "RangeError | ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH | Input buffers must have the same byte length",
		"strings": `TypeError | ERR_INVALID_ARG_TYPE | The "buf1" argument must be an instance of ArrayBuffer, Buffer, TypedArray, or DataView.`,
	})
}

// One native call cannot be interrupted, so the work a single call is asked
// for is refused up front, before any of it is done.
func TestCryptoRefusesWorkOneCallCannotFinishPromptly(t *testing.T) {
	start := time.Now()
	got := cryptoResult(t,
		"return [{ json: {",
		"  randomBytes: caught(() => crypto.randomBytes(2 ** 30)),",
		"  iterations: caught(() => crypto.pbkdf2Sync('p', 's', 2 ** 31 - 1, 64, 'sha512')),",
		"  blocks: caught(() => crypto.pbkdf2Sync('p', 's', 100000, 20 * 64, 'sha1')),",
		"  pbkdf2Key: caught(() => crypto.pbkdf2Sync('p', 's', 1, 2 ** 27, 'sha1')),",
		"  scryptMemory: caught(() => crypto.scryptSync('p', 's', 64, { N: 2 ** 17, r: 8, maxmem: 2 ** 31 })),",
		"  scryptCost: caught(() => crypto.scryptSync('p', 's', 64, { N: 2 ** 14, r: 8, p: 16, maxmem: 2 ** 31 })),",
		"  scryptKey: caught(() => crypto.scryptSync('p', 's', 2 ** 27, { N: 2 })),",
		"  allowed: crypto.pbkdf2Sync('p', 's', 210000, 64, 'sha512').length,",
		"} }]",
	)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("refusing took %v; each refusal should come before any work", elapsed)
	}
	for key, want := range map[string]string{
		"randomBytes":  "RangeError |  | a buffer of bytes of 1073741824 is more than the 67108864 one call may handle here",
		"iterations":   "RangeError |  | a pbkdf2 derivation of 2147483648 rounds is more than the 2097152 one call may run here",
		"blocks":       "RangeError |  | a pbkdf2 derivation of 6400064 rounds is more than the 2097152 one call may run here",
		"pbkdf2Key":    "RangeError |  | a buffer of bytes of 134217728 is more than the 67108864 one call may handle here",
		"scryptMemory": "RangeError |  | scrypt with N of 131072 and r of 8 needs 134217728 bytes of memory, more than the 67108864 one call may use here",
		"scryptCost":   "RangeError |  | scrypt's cost N × r × p, 16384 × 8 × 16, is more than the 1048576 one call may run here",
		"scryptKey":    "RangeError |  | a buffer of bytes of 134217728 is more than the 67108864 one call may handle here",
	} {
		if text, _ := got[key].(string); !strings.HasPrefix(text, want) {
			t.Errorf("%s = %v\n\twant it to start %q", key, got[key], want)
		}
	}
	if got["allowed"] != float64(64) {
		t.Errorf("OWASP's 210,000 iterations of PBKDF2-HMAC-SHA512 gave %v, want a 64-byte key", got["allowed"])
	}
}

func TestCryptoIsOneModuleUnderEitherNameAndAGlobal(t *testing.T) {
	refusal := func(subject string) string {
		return "Error |  | " + jsrun.Refusal(subject, jsrun.UnsupportedAdvice)
	}
	got := cryptoResult(t,
		"return [{ json: {",
		"  same: require('crypto') === require('node:crypto'),",
		"  global: globalThis.crypto === crypto.webcrypto,",
		"  globalKeys: Object.keys(globalThis.crypto),",
		"  subtle: caught(() => crypto.subtle),",
		"  globalSubtle: caught(() => globalThis.crypto.subtle.digest('SHA-256', new Uint8Array(1))),",
		"  sign: caught(() => crypto.createSign('sha256')),",
		"  ciphers: crypto.getCiphers(),",
		"} }]",
	)
	expectFields(t, got, map[string]any{
		"same":         true,
		"global":       true,
		"globalKeys":   []string{},
		"subtle":       refusal("uses crypto.subtle"),
		"globalSubtle": refusal("uses crypto.subtle"),
		"sign":         refusal("uses crypto.createSign"),
		"ciphers": []string{"aes-128-cbc", "aes-128-ctr", "aes-128-gcm", "aes-192-cbc", "aes-192-ctr", "aes-192-gcm",
			"aes-256-cbc", "aes-256-ctr", "aes-256-gcm", "aes128", "aes192", "aes256", "id-aes128-GCM", "id-aes192-GCM", "id-aes256-GCM"},
	})
}
