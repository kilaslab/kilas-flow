package jsrun

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
	"math/big"
	"sort"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// The cryptography behind the runtime's crypto module (js/modules/crypto.js):
// Node's hashes and HMACs, random values, PBKDF2, scrypt, and AES in CBC, CTR
// and GCM, done by Go's own packages. The module gives them Node's shape.
//
// A native keeps nothing between calls, so a hash or cipher in progress keeps
// its state in the JavaScript object the code holds, and hands it back on
// every call:
//
//   - a hash's state is Go's marshalled digest state, and an HMAC's is two of
//     them, the inner and the outer hash of RFC 2104;
//   - CBC hands over its chaining block, and CTR and GCM their offset into the
//     key stream.
//
// One native call cannot be interrupted, so each checks its sizes, and the
// cost of a key derivation, before it does any work.

const (
	// MaxPBKDF2Rounds bounds one PBKDF2 derivation. A round is one
	// application of the HMAC: each block of output takes as many rounds as
	// there are iterations, plus one for each 64 bytes of salt. OWASP's
	// recommended iteration counts fit for a key of one block (1,300,000 for
	// SHA-1, 600,000 for SHA-256, 210,000 for SHA-512). The most it allows
	// takes about half a second with SHA-512 on an Apple M4, and a few times
	// that on a slow core.
	MaxPBKDF2Rounds = 1 << 21
	// MaxScryptCost bounds scrypt's N × r × p, which its time is proportional
	// to. Node's defaults cost 1 << 17 (about 20 ms on an Apple M4); this is
	// eight times that. Its memory, 128 × N × r bytes, is bounded by
	// MaxBytesPerCall, and the PBKDF2 steps inside it by MaxPBKDF2Rounds.
	MaxScryptCost = 1 << 20
	// maxHashState bounds a marshalled hash state handed back from
	// JavaScript; a real one is a few hundred bytes.
	maxHashState = 1 << 12
)

func init() {
	registerNative("crypto.encoding", func(args []any) (any, error) {
		name, err := argString(args, 0, "the encoding")
		if err != nil {
			return nil, err
		}
		return normalEncoding(name), nil
	})
	registerNative("crypto.names", func([]any) (any, error) {
		return map[string]any{"hashes": digestListed, "ciphers": cipherListed}, nil
	})
	registerHashes()
	registerRandom()
	registerKeyDerivation()
	registerCiphers()
}

// ---- Arguments ---------------------------------------------------------------

// argView reads bytes a native only reads during the call, without copying
// them. The slice's capacity is its length, so an append always copies and
// can never write past it into the script's memory.
func argView(args []any, index int, name string) ([]byte, error) {
	switch value := argument(args, index).(type) {
	case []byte:
		if len(value) > MaxBytesPerCall {
			return nil, tooManyBytes(len(value))
		}
		return value[:len(value):len(value)], nil
	case string:
		if len(value) > MaxBytesPerCall {
			return nil, tooManyBytes(len(value))
		}
	}
	return argBytes(args, index, name)
}

// argFlag reads a boolean argument; anything else is false.
func argFlag(args []any, index int) bool {
	flag, _ := argument(args, index).(bool)
	return flag
}

// tooManyBytes is the runtime's per-call refusal, in the words the module's
// own allocation bounds use.
func tooManyBytes(size int) error {
	return rangeError("a buffer of bytes of %d is more than the %d one call may handle here", size, MaxBytesPerCall)
}

// ---- Hashes and HMACs ----------------------------------------------------------

type digestAlgorithm struct {
	name string
	new  func() hash.Hash
}

var digestAlgorithms = map[string]digestAlgorithm{
	"md5":    {"md5", md5.New},
	"sha1":   {"sha1", sha1.New},
	"sha256": {"sha256", sha256.New},
	"sha384": {"sha384", sha512.New384},
	"sha512": {"sha512", sha512.New},
}

// digestNames are the names Node's getHashes() lists for these digests, and
// digestAliases further names it accepts for them. Node matches both without
// regard to case.
var (
	digestNames = map[string]string{
		"RSA-MD5": "md5", "md5": "md5", "md5WithRSAEncryption": "md5", "ssl3-md5": "md5",
		"RSA-SHA1": "sha1", "RSA-SHA1-2": "sha1", "sha1": "sha1", "sha1WithRSAEncryption": "sha1", "ssl3-sha1": "sha1",
		"RSA-SHA256": "sha256", "sha256": "sha256", "sha256WithRSAEncryption": "sha256",
		"RSA-SHA384": "sha384", "sha384": "sha384", "sha384WithRSAEncryption": "sha384",
		"RSA-SHA512": "sha512", "sha512": "sha512", "sha512WithRSAEncryption": "sha512",
	}
	digestAliases = map[string]string{
		"sha-1": "sha1", "sha-256": "sha256", "sha2-256": "sha256", "sha-384": "sha384", "sha2-384": "sha384",
		"sha-512": "sha512", "sha2-512": "sha512",
	}
	digestLookup = lowerKeys(digestNames, digestAliases)
	digestListed = sortedKeys(digestNames)
)

func lowerKeys(tables ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, table := range tables {
		for name, value := range table {
			out[strings.ToLower(name)] = value
		}
	}
	return out
}

func sortedKeys[V any](table map[string]V) []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func digestNamed(name string) (digestAlgorithm, bool) {
	algorithm, ok := digestAlgorithms[digestLookup[strings.ToLower(name)]]
	return algorithm, ok
}

// argDigest reads a digest name the module has already checked.
func argDigest(args []any, index int) (digestAlgorithm, error) {
	name, err := argString(args, index, "the digest")
	if err != nil {
		return digestAlgorithm{}, err
	}
	algorithm, ok := digestNamed(name)
	if !ok {
		return digestAlgorithm{}, typeError("Invalid digest: %s", name)
	}
	return algorithm, nil
}

func saveHash(running hash.Hash) ([]byte, error) {
	return running.(encoding.BinaryMarshaler).MarshalBinary()
}

func restoreHash(algorithm digestAlgorithm, state []byte) (hash.Hash, error) {
	running := algorithm.new()
	if len(state) > maxHashState || running.(encoding.BinaryUnmarshaler).UnmarshalBinary(state) != nil {
		return nil, typeError("the %s state is not valid", algorithm.name)
	}
	return running, nil
}

// splitHMAC separates an HMAC's state into its inner and outer hashes, which
// marshal to the same length.
func splitHMAC(algorithm digestAlgorithm, state []byte) (inner, outer hash.Hash, err error) {
	if len(state)%2 != 0 {
		return nil, nil, typeError("the %s HMAC state is not valid", algorithm.name)
	}
	if inner, err = restoreHash(algorithm, state[:len(state)/2]); err != nil {
		return nil, nil, err
	}
	if outer, err = restoreHash(algorithm, state[len(state)/2:]); err != nil {
		return nil, nil, err
	}
	return inner, outer, nil
}

func saveHMAC(inner, outer hash.Hash) ([]byte, error) {
	innerState, err := saveHash(inner)
	if err != nil {
		return nil, err
	}
	outerState, err := saveHash(outer)
	if err != nil {
		return nil, err
	}
	return append(innerState, outerState...), nil
}

// startHMAC keys an HMAC as RFC 2104 does: a key longer than the hash's block
// is hashed first, the key is padded to a block, and the inner and outer
// hashes start from it XORed with their pads.
func startHMAC(algorithm digestAlgorithm, key []byte) ([]byte, error) {
	inner, outer := algorithm.new(), algorithm.new()
	size := inner.BlockSize()
	if len(key) > size {
		keyHash := algorithm.new()
		keyHash.Write(key)
		key = keyHash.Sum(nil)
	}
	pad := make([]byte, size)
	copy(pad, key)
	for index := range pad {
		pad[index] ^= 0x36
	}
	inner.Write(pad)
	for index := range pad {
		pad[index] ^= 0x36 ^ 0x5c
	}
	outer.Write(pad)
	return saveHMAC(inner, outer)
}

func registerHashes() {
	// crypto.hashInit(name) starts a hash, or returns undefined for a digest
	// this runtime does not have: createHash and createHmac word that
	// differently, so the module throws.
	registerNative("crypto.hashInit", func(args []any) (any, error) {
		name, err := argString(args, 0, "the algorithm")
		if err != nil {
			return nil, err
		}
		algorithm, ok := digestNamed(name)
		if !ok {
			return nil, nil
		}
		return saveHash(algorithm.new())
	})
	registerNative("crypto.hashUpdate", func(args []any) (any, error) {
		algorithm, err := argDigest(args, 0)
		if err != nil {
			return nil, err
		}
		state, err := argView(args, 1, "the hash state")
		if err != nil {
			return nil, err
		}
		data, err := argView(args, 2, "the data")
		if err != nil {
			return nil, err
		}
		running, err := restoreHash(algorithm, state)
		if err != nil {
			return nil, err
		}
		running.Write(data)
		return saveHash(running)
	})
	registerNative("crypto.hashDigest", func(args []any) (any, error) {
		algorithm, err := argDigest(args, 0)
		if err != nil {
			return nil, err
		}
		state, err := argView(args, 1, "the hash state")
		if err != nil {
			return nil, err
		}
		running, err := restoreHash(algorithm, state)
		if err != nil {
			return nil, err
		}
		return running.Sum(nil), nil
	})
	registerNative("crypto.hmacInit", func(args []any) (any, error) {
		name, err := argString(args, 0, "the digest")
		if err != nil {
			return nil, err
		}
		key, err := argView(args, 1, "the key")
		if err != nil {
			return nil, err
		}
		algorithm, ok := digestNamed(name)
		if !ok {
			return nil, nil
		}
		return startHMAC(algorithm, key)
	})
	registerNative("crypto.hmacUpdate", func(args []any) (any, error) {
		algorithm, err := argDigest(args, 0)
		if err != nil {
			return nil, err
		}
		state, err := argView(args, 1, "the HMAC state")
		if err != nil {
			return nil, err
		}
		data, err := argView(args, 2, "the data")
		if err != nil {
			return nil, err
		}
		inner, outer, err := splitHMAC(algorithm, state)
		if err != nil {
			return nil, err
		}
		inner.Write(data)
		return saveHMAC(inner, outer)
	})
	registerNative("crypto.hmacDigest", func(args []any) (any, error) {
		algorithm, err := argDigest(args, 0)
		if err != nil {
			return nil, err
		}
		state, err := argView(args, 1, "the HMAC state")
		if err != nil {
			return nil, err
		}
		inner, outer, err := splitHMAC(algorithm, state)
		if err != nil {
			return nil, err
		}
		outer.Write(inner.Sum(nil))
		return outer.Sum(nil), nil
	})
}

// ---- Random values ---------------------------------------------------------------

// maxRandomRange is Node's bound on randomInt's max - min: 2**48 - 1.
const maxRandomRange = 1<<48 - 1

func registerRandom() {
	registerNative("crypto.randomBytes", func(args []any) (any, error) {
		size, err := argInt(args, 0, "the size", 0, MaxBytesPerCall)
		if err != nil {
			return nil, err
		}
		out := make([]byte, size)
		rand.Read(out)
		return out, nil
	})
	// crypto.randomUUID() is a version 4 UUID (RFC 9562): 122 random bits.
	registerNative("crypto.randomUUID", func([]any) (any, error) {
		var id [16]byte
		rand.Read(id[:])
		id[6] = id[6]&0x0f | 0x40
		id[8] = id[8]&0x3f | 0x80
		text := hex.EncodeToString(id[:])
		return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
	})
	// crypto.randomInt(min, max) is uniform in [min, max).
	registerNative("crypto.randomInt", func(args []any) (any, error) {
		low, err := argInt(args, 0, "min", -(1<<53 - 1), 1<<53-1)
		if err != nil {
			return nil, err
		}
		high, err := argInt(args, 1, "max", -(1<<53 - 1), 1<<53-1)
		if err != nil {
			return nil, err
		}
		if high <= low || high-low > maxRandomRange {
			return nil, rangeError("max - min must be from 1 to %d", int64(maxRandomRange))
		}
		pick, err := rand.Int(rand.Reader, big.NewInt(high-low))
		if err != nil {
			return nil, err
		}
		return low + pick.Int64(), nil
	})
	registerNative("crypto.timingSafeEqual", func(args []any) (any, error) {
		left, err := argView(args, 0, "the first buffer")
		if err != nil {
			return nil, err
		}
		right, err := argView(args, 1, "the second buffer")
		if err != nil {
			return nil, err
		}
		if len(left) != len(right) {
			return nil, rangeError("Input buffers must have the same byte length")
		}
		return subtle.ConstantTimeCompare(left, right) == 1, nil
	})
}

// ---- Key derivation ------------------------------------------------------------

// pbkdf2Rounds counts the HMAC applications a PBKDF2 derivation makes: for
// each block of output, its iterations and one per 64 bytes of the salt and
// block index. It saturates rather than overflow.
func pbkdf2Rounds(iterations, keyLength, digestSize, saltLength int64) (rounds, blocks int64) {
	blocks = (keyLength + digestSize - 1) / digestSize
	perBlock := iterations + (saltLength+4+63)/64
	if blocks > 0 && perBlock > (math.MaxInt64/blocks) {
		return math.MaxInt64, blocks
	}
	return blocks * perBlock, blocks
}

func checkPBKDF2(what string, iterations, keyLength, digestSize, saltLength int64) error {
	rounds, blocks := pbkdf2Rounds(iterations, keyLength, digestSize, saltLength)
	if rounds > MaxPBKDF2Rounds {
		return rangeError("%s of %d rounds is more than the %d one call may run here (%d iterations, and a round per 64 bytes of salt, for each of %d blocks of output)",
			what, rounds, MaxPBKDF2Rounds, iterations, blocks)
	}
	return nil
}

// cappedProduct multiplies without overflowing, answering limit+1 for any
// product past limit.
func cappedProduct(limit int64, factors ...int64) int64 {
	product := int64(1)
	for _, factor := range factors {
		if factor != 0 && product > limit/factor {
			return limit + 1
		}
		product *= factor
	}
	return product
}

func registerKeyDerivation() {
	// crypto.pbkdf2(password, salt, iterations, keylen, digest) returns
	// undefined for a digest this runtime does not have; the module throws
	// Node's error for that.
	registerNative("crypto.pbkdf2", func(args []any) (any, error) {
		password, err := argView(args, 0, "the password")
		if err != nil {
			return nil, err
		}
		salt, err := argView(args, 1, "the salt")
		if err != nil {
			return nil, err
		}
		iterations, err := argInt(args, 2, "iterations", 1, math.MaxInt32)
		if err != nil {
			return nil, err
		}
		keyLength, err := argInt(args, 3, "keylen", 0, math.MaxInt32)
		if err != nil {
			return nil, err
		}
		name, err := argString(args, 4, "the digest")
		if err != nil {
			return nil, err
		}
		algorithm, ok := digestNamed(name)
		if !ok {
			return nil, nil
		}
		if keyLength > MaxBytesPerCall {
			return nil, tooManyBytes(int(keyLength))
		}
		if err := checkPBKDF2("a pbkdf2 derivation", iterations, keyLength, int64(algorithm.new().Size()), int64(len(salt))); err != nil {
			return nil, err
		}
		if keyLength == 0 {
			return []byte{}, nil
		}
		return pbkdf2.Key(algorithm.new, string(password), salt, int(iterations), int(keyLength))
	})
	// crypto.scrypt(password, salt, keylen, N, r, p) checks this runtime's
	// bounds; Node's own rules on the parameters, maxmem among them, are the
	// module's to check.
	registerNative("crypto.scrypt", func(args []any) (any, error) {
		password, err := argView(args, 0, "the password")
		if err != nil {
			return nil, err
		}
		salt, err := argView(args, 1, "the salt")
		if err != nil {
			return nil, err
		}
		keyLength, err := argInt(args, 2, "keylen", 0, math.MaxInt32)
		if err != nil {
			return nil, err
		}
		cost, err := argInt(args, 3, "N", 2, math.MaxUint32)
		if err != nil {
			return nil, err
		}
		blockSize, err := argInt(args, 4, "r", 1, math.MaxUint32)
		if err != nil {
			return nil, err
		}
		parallel, err := argInt(args, 5, "p", 1, math.MaxUint32)
		if err != nil {
			return nil, err
		}
		if cost&(cost-1) != 0 {
			return nil, rangeError("Invalid scrypt params")
		}
		if keyLength > MaxBytesPerCall {
			return nil, tooManyBytes(int(keyLength))
		}
		if cappedProduct(MaxScryptCost, cost, blockSize, parallel) > MaxScryptCost {
			return nil, rangeError("scrypt's cost N × r × p, %d × %d × %d, is more than the %d one call may run here", cost, blockSize, parallel, MaxScryptCost)
		}
		// Within that cost, 128 × N × r cannot overflow.
		if memory := 128 * cost * blockSize; memory > MaxBytesPerCall {
			return nil, rangeError("scrypt with N of %d and r of %d needs %d bytes of memory, more than the %d one call may use here", cost, blockSize, memory, MaxBytesPerCall)
		}
		// scrypt runs PBKDF2-HMAC-SHA256 twice: over the salt to fill its
		// 128 × r × p bytes of blocks, then over those blocks for the key.
		mixed := 128 * blockSize * parallel
		if err := checkPBKDF2("scrypt's first pbkdf2 step", 1, mixed, sha256.Size, int64(len(salt))); err != nil {
			return nil, err
		}
		if err := checkPBKDF2("scrypt's last pbkdf2 step", 1, keyLength, sha256.Size, mixed); err != nil {
			return nil, err
		}
		if keyLength == 0 {
			return []byte{}, nil
		}
		key, err := scrypt.Key(password, salt, int(cost), int(blockSize), int(parallel), int(keyLength))
		if err != nil {
			return nil, rangeError("Invalid scrypt params: %s", err.Error())
		}
		return key, nil
	})
}

// ---- AES --------------------------------------------------------------------------

type cipherSpec struct {
	mode      string
	keyLength int
}

// cipherNames are the AES ciphers this runtime has, as Node's getCiphers()
// lists them; Node matches them without regard to case. aes128, aes192 and
// aes256 are OpenSSL's names for CBC.
var (
	cipherNames = map[string]cipherSpec{
		"aes-128-cbc": {"cbc", 16}, "aes-192-cbc": {"cbc", 24}, "aes-256-cbc": {"cbc", 32},
		"aes-128-ctr": {"ctr", 16}, "aes-192-ctr": {"ctr", 24}, "aes-256-ctr": {"ctr", 32},
		"aes-128-gcm": {"gcm", 16}, "aes-192-gcm": {"gcm", 24}, "aes-256-gcm": {"gcm", 32},
		"aes128": {"cbc", 16}, "aes192": {"cbc", 24}, "aes256": {"cbc", 32},
		"id-aes128-GCM": {"gcm", 16}, "id-aes192-GCM": {"gcm", 24}, "id-aes256-GCM": {"gcm", 32},
	}
	cipherLookup = func() map[string]cipherSpec {
		out := map[string]cipherSpec{}
		for name, spec := range cipherNames {
			out[strings.ToLower(name)] = spec
		}
		return out
	}()
	cipherListed = sortedKeys(cipherNames)
)

// GCM's IV may be 1 to 128 bytes in Node, and its tag 4, 8 or 12 to 16.
const (
	maxGCMIV       = 128
	fullGCMTag     = 16
	shortestGCMTag = 4
)

func validGCMTag(length int) bool {
	return length == 4 || length == 8 || (length >= 12 && length <= fullGCMTag)
}

func argAES(args []any, index int) (cipher.Block, error) {
	key, err := argView(args, index, "the key")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, rangeError("Invalid key length")
	}
	return block, nil
}

// argIV reads an IV of the given length range.
func argIV(args []any, index, shortest, longest int) ([]byte, error) {
	iv, err := argView(args, index, "the IV")
	if err != nil {
		return nil, err
	}
	if len(iv) < shortest || len(iv) > longest {
		return nil, typeError("Invalid initialization vector")
	}
	return iv, nil
}

// argGCM reads a key and IV for GCM.
func argGCM(args []any) (cipher.Block, cipher.AEAD, []byte, error) {
	block, err := argAES(args, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	iv, err := argIV(args, 1, 1, maxGCMIV)
	if err != nil {
		return nil, nil, nil, err
	}
	aead, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return nil, nil, nil, typeError("Invalid initialization vector")
	}
	return block, aead, iv, nil
}

// gcmCounter finds the counter block GCM encrypts a message's first block
// under, for an IV of any length (NIST SP 800-38D: inc32 of J0). GCM is
// counter mode from that block, so sealing one block of zeros yields the
// block encrypted, and AES decrypts it back. With it GCM's key stream can be
// produced from any offset, and update() can return output as it goes, as
// Node's does.
func gcmCounter(block cipher.Block, aead cipher.AEAD, iv []byte) [aes.BlockSize]byte {
	var zero, counter [aes.BlockSize]byte
	sealed := aead.Seal(nil, iv, zero[:], nil)
	block.Decrypt(counter[:], sealed[:aes.BlockSize])
	return counter
}

// addCounter moves a counter block on by some number of blocks. CTR mode
// counts in all 128 bits, as Node's does; GCM counts in the low 32 only, and
// wraps there.
func addCounter(counter [aes.BlockSize]byte, blocks uint64, low32 bool) [aes.BlockSize]byte {
	if low32 {
		binary.BigEndian.PutUint32(counter[12:], binary.BigEndian.Uint32(counter[12:])+uint32(blocks))
		return counter
	}
	low := binary.BigEndian.Uint64(counter[8:])
	sum := low + blocks
	binary.BigEndian.PutUint64(counter[8:], sum)
	if sum < low {
		binary.BigEndian.PutUint64(counter[:8], binary.BigEndian.Uint64(counter[:8])+1)
	}
	return counter
}

// counterXOR runs AES in counter mode over data, offset bytes into the key
// stream that starts at counter block first. Go's counter mode counts in all
// 128 bits, so for GCM the stream is made in runs that end where the low 32
// bits wrap.
func counterXOR(block cipher.Block, first [aes.BlockSize]byte, offset uint64, data []byte, low32 bool) []byte {
	out := make([]byte, len(data))
	for done := 0; done < len(data); {
		position := offset + uint64(done)
		counter := addCounter(first, position/aes.BlockSize, low32)
		skip := int(position % aes.BlockSize)
		run := len(data) - done
		if low32 {
			left := (1<<32-uint64(binary.BigEndian.Uint32(counter[12:])))*aes.BlockSize - uint64(skip)
			if uint64(run) > left {
				run = int(left)
			}
		}
		stream := cipher.NewCTR(block, counter[:])
		if skip > 0 {
			var discard [aes.BlockSize]byte
			stream.XORKeyStream(discard[:skip], discard[:skip])
		}
		stream.XORKeyStream(out[done:done+run], data[done:done+run])
		done += run
	}
	return out
}

func pkcs7Pad(data []byte) []byte {
	padding := aes.BlockSize - len(data)%aes.BlockSize
	padded := make([]byte, len(data)+padding)
	copy(padded, data)
	for index := len(data); index < len(padded); index++ {
		padded[index] = byte(padding)
	}
	return padded
}

// pkcs7Unpad removes PKCS#7 padding, reporting whether it was well formed.
func pkcs7Unpad(data []byte) ([]byte, bool) {
	if len(data) == 0 {
		return nil, false
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(data) {
		return nil, false
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, false
		}
	}
	return data[:len(data)-padding], true
}

func registerCiphers() {
	// crypto.cipherInfo(name) is {mode, keyLength} for a cipher this runtime
	// has, and undefined for any other.
	registerNative("crypto.cipherInfo", func(args []any) (any, error) {
		name, err := argString(args, 0, "the cipher")
		if err != nil {
			return nil, err
		}
		spec, ok := cipherLookup[strings.ToLower(name)]
		if !ok {
			return nil, nil
		}
		return map[string]any{"mode": spec.mode, "keyLength": spec.keyLength}, nil
	})
	// crypto.cbc(key, iv, data, decrypt, last) runs CBC over whole blocks.
	// With last set it adds PKCS#7 padding before encrypting, or checks and
	// removes it after decrypting, returning undefined when the padding is
	// wrong: the module throws Node's error for that.
	registerNative("crypto.cbc", func(args []any) (any, error) {
		block, err := argAES(args, 0)
		if err != nil {
			return nil, err
		}
		iv, err := argIV(args, 1, aes.BlockSize, aes.BlockSize)
		if err != nil {
			return nil, err
		}
		data, err := argView(args, 2, "the data")
		if err != nil {
			return nil, err
		}
		decrypt, last := argFlag(args, 3), argFlag(args, 4)
		if last && !decrypt {
			data = pkcs7Pad(data)
		}
		if len(data)%aes.BlockSize != 0 {
			return nil, rangeError("CBC works in whole blocks of %d bytes, and was given %d", aes.BlockSize, len(data))
		}
		out := make([]byte, len(data))
		if decrypt {
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
		} else {
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
		}
		if last && decrypt {
			plain, ok := pkcs7Unpad(out)
			if !ok {
				return nil, nil
			}
			return plain, nil
		}
		return out, nil
	})
	// crypto.ctr(key, iv, offset, data) runs CTR from offset bytes into the
	// stream.
	registerNative("crypto.ctr", func(args []any) (any, error) {
		block, err := argAES(args, 0)
		if err != nil {
			return nil, err
		}
		iv, err := argIV(args, 1, aes.BlockSize, aes.BlockSize)
		if err != nil {
			return nil, err
		}
		offset, err := argInt(args, 2, "the offset", 0, 1<<53)
		if err != nil {
			return nil, err
		}
		data, err := argView(args, 3, "the data")
		if err != nil {
			return nil, err
		}
		return counterXOR(block, [aes.BlockSize]byte(iv), uint64(offset), data, false), nil
	})
	// crypto.gcm(key, iv, offset, data) encrypts or decrypts part of a GCM
	// message, offset bytes into it. A GCM message is bounded by
	// MaxBytesPerCall, since its tag is computed over the whole of it at once.
	registerNative("crypto.gcm", func(args []any) (any, error) {
		block, aead, iv, err := argGCM(args)
		if err != nil {
			return nil, err
		}
		offset, err := argInt(args, 2, "the offset", 0, MaxBytesPerCall)
		if err != nil {
			return nil, err
		}
		data, err := argView(args, 3, "the data")
		if err != nil {
			return nil, err
		}
		if int(offset)+len(data) > MaxBytesPerCall {
			return nil, tooManyBytes(int(offset) + len(data))
		}
		return counterXOR(block, gcmCounter(block, aead, iv), uint64(offset), data, true), nil
	})
	// crypto.gcmTag(key, iv, aad, plaintext, tagLength) is the tag of a whole
	// message. A shorter tag is the full tag's first bytes (SP 800-38D).
	registerNative("crypto.gcmTag", func(args []any) (any, error) {
		_, aead, iv, err := argGCM(args)
		if err != nil {
			return nil, err
		}
		aad, err := argView(args, 2, "the additional data")
		if err != nil {
			return nil, err
		}
		plaintext, err := argView(args, 3, "the message")
		if err != nil {
			return nil, err
		}
		size, err := argInt(args, 4, "the tag length", shortestGCMTag, fullGCMTag)
		if err != nil {
			return nil, err
		}
		if !validGCMTag(int(size)) {
			return nil, typeError("Invalid authentication tag length: %d", size)
		}
		sealed := aead.Seal(nil, iv, plaintext, aad)
		return sealed[len(plaintext) : len(plaintext)+int(size)], nil
	})
	// crypto.gcmCheck(key, iv, aad, ciphertext, tag) reports whether the tag
	// authenticates the whole message. A full tag is checked by Go's Open; a
	// shortened one against the first bytes of the full tag, recomputed from
	// the plaintext, in constant time.
	registerNative("crypto.gcmCheck", func(args []any) (any, error) {
		block, aead, iv, err := argGCM(args)
		if err != nil {
			return nil, err
		}
		aad, err := argView(args, 2, "the additional data")
		if err != nil {
			return nil, err
		}
		ciphertext, err := argView(args, 3, "the message")
		if err != nil {
			return nil, err
		}
		tag, err := argView(args, 4, "the tag")
		if err != nil {
			return nil, err
		}
		if !validGCMTag(len(tag)) {
			return nil, typeError("Invalid authentication tag length: %d", len(tag))
		}
		if len(tag) == fullGCMTag {
			_, err := aead.Open(nil, iv, append(ciphertext, tag...), aad)
			return err == nil, nil
		}
		plaintext := counterXOR(block, gcmCounter(block, aead, iv), 0, ciphertext, true)
		sealed := aead.Seal(nil, iv, plaintext, aad)
		return subtle.ConstantTimeCompare(sealed[len(plaintext):len(plaintext)+len(tag)], tag) == 1, nil
	})
}
