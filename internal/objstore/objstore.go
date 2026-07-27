// Package objstore is the content-addressed object store: a hash-keyed
// put/get map with deduplication, an on-disk layout under a configurable root,
// and codec-epoch awareness.
//
// It is the single implementation of law L2 (content identity /
// extensionality) as code: id = hash(content); get(put(x)) = x; puts are
// idempotent; identity is location-free (the same content yields the same id
// in any root). Because ids are epoch-relative, every stored object records
// the codec version that produced it, and a read through a store opened at a
// different epoch is a hard error rather than a silent mis-decode.
//
// The package is pure kernel: it depends only on the filesystem and the
// standard library. It never imports migrate, introspect, or serve. Multiple
// roots (migrations/objects/ now, imports/<alias>/ later) are supported by
// constructing multiple Store values, one per root.
package objstore

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotFound is returned by Get when no object with the given id exists.
var ErrNotFound = errors.New("objstore: object not found")

// EpochMismatch is returned by Get when an object was stored under a codec
// epoch different from the one the reading Store was opened with. Reading such
// an object would mis-decode content produced by a different encoder, so it is
// a hard error and never a silent fallthrough.
type EpochMismatch struct {
	ID   string // content id of the offending object
	Want uint32 // epoch the reading Store expects
	Got  uint32 // epoch recorded on disk
}

func (e *EpochMismatch) Error() string {
	return fmt.Sprintf("objstore: epoch mismatch reading %s: store epoch %d, object epoch %d", e.ID, e.Want, e.Got)
}

// CorruptObject is returned when a stored object's header is unreadable or its
// content no longer hashes to its id (on-disk corruption).
type CorruptObject struct {
	ID     string
	Reason string
}

func (e *CorruptObject) Error() string {
	return fmt.Sprintf("objstore: corrupt object %s: %s", e.ID, e.Reason)
}

// magic identifies the on-disk object envelope and its format version. It is
// distinct from the codec epoch: magic versions the container, epoch versions
// the content codec. The layout is:
//
//	[4]byte magic "PGO1" | [4]byte big-endian epoch | content bytes...
//
// The id is hash(content) only — the envelope is not hashed, so identity is
// location- and epoch-independent (two stores at different epochs assign the
// same id to the same content; the epoch guards decoding, not identity).
var magic = [4]byte{'P', 'G', 'O', '1'}

const headerLen = 8 // len(magic) + 4-byte epoch

// Store is a content-addressed object store rooted at a single directory and
// bound to a single codec epoch. It is safe for concurrent use.
type Store struct {
	root  string
	epoch uint32
}

// New opens (creating if necessary) a content-addressed store rooted at root
// and bound to the given codec epoch. All objects written through this Store
// carry that epoch; reads verify it.
func New(root string, epoch uint32) (*Store, error) {
	if root == "" {
		return nil, errors.New("objstore: root path is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("objstore: creating root %q: %w", root, err)
	}
	return &Store{root: root, epoch: epoch}, nil
}

// Root returns the store's root directory.
func (s *Store) Root() string { return s.root }

// Epoch returns the codec epoch this store is bound to.
func (s *Store) Epoch() uint32 { return s.epoch }

// ID returns the content id (lowercase SHA-256 hex) of content. It is a pure
// function of the bytes: the same content yields the same id everywhere, which
// is what makes identity location-free.
func ID(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// objectPath maps a content id to its on-disk path using a git-style two-hex
// fanout to keep directories small: root/ab/cdef...
func (s *Store) objectPath(id string) string {
	return filepath.Join(s.root, id[:2], id[2:])
}

// Put stores content and returns its content id. Puts are idempotent: storing
// the same bytes twice yields the same id, no error, and no duplicate on disk.
// Because the path is content-derived, concurrent puts of the same content
// converge on one object.
func (s *Store) Put(content []byte) (string, error) {
	id := ID(content)
	path := s.objectPath(id)

	// Idempotence / dedup: if an object with this id already exists, the
	// content is identical by construction (id = hash(content)), so there is
	// nothing to write. We do not rewrite it and do not error.
	if _, err := os.Stat(path); err == nil {
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("objstore: stat %s: %w", id, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("objstore: creating object dir for %s: %w", id, err)
	}

	// Build the envelope: magic | epoch | content.
	buf := make([]byte, headerLen+len(content))
	copy(buf[0:4], magic[:])
	binary.BigEndian.PutUint32(buf[4:8], s.epoch)
	copy(buf[headerLen:], content)

	// Write to a unique temp file in the same directory, then atomically rename
	// over the target. Concurrent writers each stage their own temp file; the
	// final rename is atomic and, since every writer stages identical bytes,
	// the winner's object is correct regardless of ordering.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+id[2:]+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("objstore: temp file for %s: %w", id, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("objstore: writing %s: %w", id, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("objstore: closing %s: %w", id, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("objstore: committing %s: %w", id, err)
	}
	return id, nil
}

// Has reports whether an object with the given id exists in this store's root.
func (s *Store) Has(id string) (bool, error) {
	_, err := os.Stat(s.objectPath(id))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("objstore: stat %s: %w", id, err)
}

// Get returns the content stored under id. It verifies the recorded codec
// epoch against the store's epoch (EpochMismatch on disagreement) and verifies
// that the content still hashes to its id (CorruptObject on disagreement), so
// a read never silently returns bytes produced by a different codec or a
// bit-rotted object.
func (s *Store) Get(id string) ([]byte, error) {
	path := s.objectPath(id)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("objstore: reading %s: %w", id, err)
	}
	if len(raw) < headerLen {
		return nil, &CorruptObject{ID: id, Reason: "truncated envelope header"}
	}
	if [4]byte(raw[0:4]) != magic {
		return nil, &CorruptObject{ID: id, Reason: "bad envelope magic"}
	}
	got := binary.BigEndian.Uint32(raw[4:8])
	if got != s.epoch {
		return nil, &EpochMismatch{ID: id, Want: s.epoch, Got: got}
	}
	content := raw[headerLen:]
	if ID(content) != id {
		return nil, &CorruptObject{ID: id, Reason: "content does not hash to id"}
	}
	// Return a copy so callers cannot mutate our backing slice's tail.
	out := make([]byte, len(content))
	copy(out, content)
	return out, nil
}
