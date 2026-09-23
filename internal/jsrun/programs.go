package jsrun

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// prepared is a body ready to run: analysed, and compiled when it can run.
type prepared struct {
	program  *program
	analysis Analysis
	wrapped  wrapped
}

// programCache keeps the most recently used compiled bodies. A compiled
// program holds no VM state, so one entry serves every execution of that
// body, across tenants.
type programCache struct {
	mu      sync.Mutex
	limit   int
	entries map[string]*list.Element
	order   *list.List
	hits    int
	misses  int
}

type cacheEntry struct {
	key      string
	prepared *prepared
}

// sharedPrograms is the process's one cache. Analyze and every runner share
// it, so validating a body at save time compiles it for its first run.
var sharedPrograms = newProgramCache(512)

func newProgramCache(limit int) *programCache {
	return &programCache{limit: limit, entries: map[string]*list.Element{}, order: list.New()}
}

// programKey names one compiled body. The match timeout is part of it
// because goja bakes it into every regular expression it compiles.
func programKey(source string, mode Mode) string {
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\x00%s\x00%d\x00", wrapperVersion, mode, currentMatchTimeout())
	digest.Write([]byte(source))
	return hex.EncodeToString(digest.Sum(nil))
}

// prepare parses, checks, analyses and compiles a body, or returns the cached
// result. A body that is refused or does not parse is never cached: it fails
// quickly every time, and caching it would only hold its text.
func (cache *programCache) prepare(source string, mode Mode) (*prepared, error) {
	mode = mode.orDefault()
	key := programKey(source, mode)
	if hit := cache.get(key); hit != nil {
		return hit, nil
	}
	w := wrap(source, mode)
	parsed, err := parseWrapped(w)
	if err != nil {
		return nil, err
	}
	body, err := checkShape(parsed, w)
	if err != nil {
		return nil, err
	}
	analysis := inspect(body, w)
	if len(analysis.Unsupported) > 0 {
		return &prepared{analysis: analysis, wrapped: w}, &UnsupportedError{Found: analysis.Unsupported}
	}
	compiled, err := compileProgram(parsed, w)
	if err != nil {
		return nil, err
	}
	ready := &prepared{program: compiled, analysis: analysis, wrapped: w}
	cache.put(key, ready)
	return ready, nil
}

func (cache *programCache) get(key string) *prepared {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, ok := cache.entries[key]
	if !ok {
		cache.misses++
		return nil
	}
	cache.hits++
	cache.order.MoveToFront(element)
	return element.Value.(*cacheEntry).prepared
}

func (cache *programCache) put(key string, ready *prepared) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element, ok := cache.entries[key]; ok {
		cache.order.MoveToFront(element)
		return
	}
	cache.entries[key] = cache.order.PushFront(&cacheEntry{key: key, prepared: ready})
	for cache.order.Len() > cache.limit {
		oldest := cache.order.Back()
		cache.order.Remove(oldest)
		delete(cache.entries, oldest.Value.(*cacheEntry).key)
	}
}
