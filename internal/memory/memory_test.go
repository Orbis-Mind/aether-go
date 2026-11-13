package memory

import (
	"fmt"
	"sync"
	"testing"
)

func TestVectorStoreDelete(t *testing.T) {
	vs := NewVectorStore("")
	vs.Add("k1", "hello world", nil, 3600)
	vs.Add("k2", "goodbye world", nil, 3600)
	if vs.Count() != 2 {
		t.Fatalf("count = %d, want 2", vs.Count())
	}
	if !vs.Delete("k1") {
		t.Error("Delete(k1) returned false")
	}
	if vs.Delete("missing") {
		t.Error("Delete(missing) returned true")
	}
	if vs.Count() != 1 {
		t.Errorf("count after delete = %d, want 1", vs.Count())
	}
}

func TestVectorStorePurge(t *testing.T) {
	vs := NewVectorStore("")
	vs.Add("a", "one", nil, 3600)
	vs.Add("b", "two", nil, 3600)
	if n := vs.Purge(); n != 2 {
		t.Errorf("Purge removed %d, want 2", n)
	}
	if vs.Count() != 0 {
		t.Errorf("count after purge = %d, want 0", vs.Count())
	}
}

// TestVectorStoreConcurrent exercises concurrent Add/Search/Delete under -race.
func TestVectorStoreConcurrent(t *testing.T) {
	vs := NewVectorStore("")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("k-%d", n)
			vs.Add(key, fmt.Sprintf("content number %d", n), nil, 3600)
			vs.Search("content", 3, 0.0)
			vs.Count()
			if n%3 == 0 {
				vs.Delete(key)
			}
		}(i)
	}
	wg.Wait()
}

func TestEntityAndRelationshipConcurrent(t *testing.T) {
	em := NewEntityMemory()
	rm := NewRelationshipMemory()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			em.Upsert(fmt.Sprintf("entity%d", n%6), map[string]string{"role": "x"})
			em.ToContext()
			em.Count()
			rm.Add(fmt.Sprintf("a%d", n), "knows", "b", 0.9)
			rm.ToContext()
			rm.Count()
		}(i)
	}
	wg.Wait()
}
