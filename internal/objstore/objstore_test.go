package objstore

import (
	"errors"
	"github.com/smm-h/pgdesign/internal/testenv"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

// tempRoot creates a fresh temp directory and registers its removal with
// cleanup. It works from both *testing.T and *rapid.T via the cleanup hook.
func tempRoot(cleanup func(func())) string {
	dir, err := os.MkdirTemp("", "objstore")
	if err != nil {
		panic(err)
	}
	cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// countObjects walks a store root and counts stored object files (files under
// the two-hex fanout directories), ignoring staging temp files.
func countObjects(root string) (int, error) {
	n := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Temp staging files start with a dot.
		if name := d.Name(); len(name) > 0 && name[0] == '.' {
			return nil
		}
		n++
		return nil
	})
	return n, err
}

// TestGetPutIdentity is the get∘put = identity property: whatever bytes go in
// come back out unchanged.
func TestGetPutIdentity(t *testing.T) {
	testenv.Isolate(t)
	rapid.Check(t, func(rt *rapid.T) {
		store, err := New(tempRoot(rt.Cleanup), 1)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}
		content := rapid.SliceOf(rapid.Byte()).Draw(rt, "content")
		id, err := store.Put(content)
		if err != nil {
			rt.Fatalf("Put: %v", err)
		}
		got, err := store.Get(id)
		if err != nil {
			rt.Fatalf("Get: %v", err)
		}
		if string(got) != string(content) {
			rt.Fatalf("get∘put != identity: put %q got %q", content, got)
		}
	})
}

// TestPutIdempotent is the idempotence property: putting the same bytes twice
// yields the same id, no error, and exactly one object on disk.
func TestPutIdempotent(t *testing.T) {
	testenv.Isolate(t)
	rapid.Check(t, func(rt *rapid.T) {
		root := tempRoot(rt.Cleanup)
		store, err := New(root, 1)
		if err != nil {
			rt.Fatalf("New: %v", err)
		}
		content := rapid.SliceOf(rapid.Byte()).Draw(rt, "content")

		id1, err := store.Put(content)
		if err != nil {
			rt.Fatalf("first Put: %v", err)
		}
		id2, err := store.Put(content)
		if err != nil {
			rt.Fatalf("second Put: %v", err)
		}
		if id1 != id2 {
			rt.Fatalf("double-put ids differ: %s vs %s", id1, id2)
		}
		n, err := countObjects(root)
		if err != nil {
			rt.Fatalf("countObjects: %v", err)
		}
		if n != 1 {
			rt.Fatalf("double-put created %d objects, want 1", n)
		}
	})
}

// TestIDsLocationFree is the location-free-identity property: the same content
// stored in two independent roots (at the same epoch) gets the same id, and is
// retrievable from both.
func TestIDsLocationFree(t *testing.T) {
	testenv.Isolate(t)
	rapid.Check(t, func(rt *rapid.T) {
		a, err := New(tempRoot(rt.Cleanup), 1)
		if err != nil {
			rt.Fatalf("New a: %v", err)
		}
		b, err := New(tempRoot(rt.Cleanup), 1)
		if err != nil {
			rt.Fatalf("New b: %v", err)
		}
		content := rapid.SliceOf(rapid.Byte()).Draw(rt, "content")

		idA, err := a.Put(content)
		if err != nil {
			rt.Fatalf("Put a: %v", err)
		}
		idB, err := b.Put(content)
		if err != nil {
			rt.Fatalf("Put b: %v", err)
		}
		if idA != idB {
			rt.Fatalf("ids not location-free: %s (root a) vs %s (root b)", idA, idB)
		}
		if pure := ID(content); idA != pure {
			rt.Fatalf("id %s does not match pure ID() %s", idA, pure)
		}
		gotA, err := a.Get(idA)
		if err != nil {
			rt.Fatalf("Get a: %v", err)
		}
		gotB, err := b.Get(idB)
		if err != nil {
			rt.Fatalf("Get b: %v", err)
		}
		if string(gotA) != string(content) || string(gotB) != string(content) {
			rt.Fatalf("content not retrievable from both roots")
		}
	})
}

// TestConcurrentIdempotentPut races many goroutines putting the same content
// into one store. All must succeed, agree on the id, and leave exactly one
// object on disk. Run under -race to catch data races in the write path.
func TestConcurrentIdempotentPut(t *testing.T) {
	testenv.Isolate(t)
	root := t.TempDir()
	store, err := New(root, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	content := []byte("the same content, raced by many goroutines")
	want := ID(content)

	const goroutines = 64
	var wg sync.WaitGroup
	ids := make([]string, goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ids[i], errs[i] = store.Put(content)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d Put error: %v", i, errs[i])
		}
		if ids[i] != want {
			t.Fatalf("goroutine %d got id %s, want %s", i, ids[i], want)
		}
	}
	n, err := countObjects(root)
	if err != nil {
		t.Fatalf("countObjects: %v", err)
	}
	if n != 1 {
		t.Fatalf("concurrent put created %d objects, want 1", n)
	}
	got, err := store.Get(want)
	if err != nil {
		t.Fatalf("Get after concurrent put: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("content corrupted by concurrent put: got %q", got)
	}
}

// TestEpochMismatchReadErrors verifies that reading an object through a store
// opened at a different epoch is a hard error, never a silent mis-decode.
func TestEpochMismatchReadErrors(t *testing.T) {
	testenv.Isolate(t)
	rapid.Check(t, func(rt *rapid.T) {
		root := tempRoot(rt.Cleanup)
		writeEpoch := rapid.Uint32().Draw(rt, "writeEpoch")
		readEpoch := rapid.Uint32().Draw(rt, "readEpoch")
		if readEpoch == writeEpoch {
			readEpoch++ // guarantee a mismatch
		}
		content := rapid.SliceOf(rapid.Byte()).Draw(rt, "content")

		writer, err := New(root, writeEpoch)
		if err != nil {
			rt.Fatalf("New writer: %v", err)
		}
		id, err := writer.Put(content)
		if err != nil {
			rt.Fatalf("Put: %v", err)
		}

		reader, err := New(root, readEpoch)
		if err != nil {
			rt.Fatalf("New reader: %v", err)
		}
		_, err = reader.Get(id)
		if err == nil {
			rt.Fatalf("expected epoch mismatch error, got nil")
		}
		var em *EpochMismatch
		if !errors.As(err, &em) {
			rt.Fatalf("expected *EpochMismatch, got %T: %v", err, err)
		}
		if em.Want != readEpoch || em.Got != writeEpoch {
			rt.Fatalf("epoch mismatch fields wrong: want %d got %d (expected want=%d got=%d)",
				em.Want, em.Got, readEpoch, writeEpoch)
		}

		// The writer (correct epoch) still reads it back cleanly.
		back, err := writer.Get(id)
		if err != nil {
			rt.Fatalf("writer Get: %v", err)
		}
		if string(back) != string(content) {
			rt.Fatalf("writer read wrong content")
		}
	})
}

// TestGetNotFound verifies Get returns ErrNotFound for an unknown id.
func TestGetNotFound(t *testing.T) {
	testenv.Isolate(t)
	store, err := New(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = store.Get(ID([]byte("never stored")))
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestCorruptContentDetected verifies that a corrupted object body (content no
// longer hashing to its id) is caught rather than returned.
func TestCorruptContentDetected(t *testing.T) {
	testenv.Isolate(t)
	store, err := New(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id, err := store.Put([]byte("original content"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	path := store.objectPath(id)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = store.Get(id)
	var co *CorruptObject
	if !errors.As(err, &co) {
		t.Fatalf("expected *CorruptObject, got %T: %v", err, err)
	}
}
