package cache

import (
	"fmt"
	"sync"
	"testing"
)

// TestExactCacheBounded verifies the cache never exceeds maxCacheEntries.
func TestExactCacheBounded(t *testing.T) {
	c := NewExactCache("")
	for i := 0; i < maxCacheEntries+500; i++ {
		c.Set(fmt.Sprintf("prompt-%d", i), "r", 3600)
	}
	if n := len(c.data); n > maxCacheEntries {
		t.Errorf("exact cache grew to %d entries, exceeds cap %d", n, maxCacheEntries)
	}
}

// TestSemanticCacheBounded verifies the semantic cache is bounded too.
func TestSemanticCacheBounded(t *testing.T) {
	c := NewSemanticCache(semanticThreshold, "")
	for i := 0; i < maxCacheEntries+500; i++ {
		c.Set(fmt.Sprintf("unique prompt number %d here", i), "r", 3600)
	}
	if n := len(c.entries); n > maxCacheEntries {
		t.Errorf("semantic cache grew to %d entries, exceeds cap %d", n, maxCacheEntries)
	}
}

// TestExactCacheConcurrent exercises concurrent readers/writers under -race.
func TestExactCacheConcurrent(t *testing.T) {
	c := NewExactCache("") // no persistence
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("prompt-%d", n%7)
			c.Set(key, "resp", 3600)
			c.Get(key)
			c.Stats()
		}(i)
	}
	wg.Wait()
}

// TestSemanticCacheConcurrent does the same for the semantic cache.
func TestSemanticCacheConcurrent(t *testing.T) {
	c := NewSemanticCache(semanticThreshold, "")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Set(fmt.Sprintf("the quick brown fox %d", n%5), "resp", 3600)
			c.Get("the quick brown fox 1")
			c.Stats()
		}(i)
	}
	wg.Wait()
}
