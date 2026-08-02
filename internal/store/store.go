// Package store persists CouchHub's own state in a single bbolt file.
//
// bbolt rather than the managed CouchDB, because the UI has to work before any
// CouchDB is reachable - the install wizard needs somewhere to record the
// server it is about to provision, and an operator debugging a broken CouchDB
// still needs the panel to load.
package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"

	"github.com/dearmai/couch-hub/internal/secret"
)

// Bucket names. Kept as vars because bbolt wants []byte.
var (
	bucketMeta       = []byte("meta")
	bucketProfiles   = []byte("profiles")
	bucketVaults     = []byte("vaults")
	bucketMigrations = []byte("migrations")
	bucketSnapshots  = []byte("snapshots") // nested: one sub-bucket per vault
	bucketActivity   = []byte("activity")  // nested: one sub-bucket per vault

	keySalt          = []byte("secret_salt")
	keySchemaVersion = []byte("schema_version")
)

const schemaVersion = 1

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("store: not found")

type Store struct {
	db   *bbolt.DB
	salt []byte
}

// Open creates or opens the store at path, creating parent directories.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	// 1s timeout so a second instance fails fast instead of hanging on the lock.
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		for _, name := range [][]byte{
			bucketMeta, bucketProfiles, bucketVaults, bucketMigrations, bucketSnapshots, bucketActivity,
		} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("store: create bucket %s: %w", name, err)
			}
		}

		meta := tx.Bucket(bucketMeta)

		if v := meta.Get(keySchemaVersion); v == nil {
			var buf [8]byte
			binary.BigEndian.PutUint64(buf[:], schemaVersion)
			if err := meta.Put(keySchemaVersion, buf[:]); err != nil {
				return err
			}
		} else if got := binary.BigEndian.Uint64(v); got > schemaVersion {
			return fmt.Errorf("store: database schema version %d is newer than this build supports (%d)", got, schemaVersion)
		}

		salt := meta.Get(keySalt)
		if salt == nil {
			fresh, err := secret.NewSalt()
			if err != nil {
				return err
			}
			if err := meta.Put(keySalt, fresh); err != nil {
				return err
			}
			salt = fresh
		}
		// bbolt values are only valid inside the transaction.
		s.salt = append([]byte(nil), salt...)
		return nil
	})
}

// Salt returns the per-store salt for deriving the sealing key.
func (s *Store) Salt() []byte { return s.salt }

func (s *Store) Close() error { return s.db.Close() }

// --- generic record helpers ------------------------------------------------

func put[T any](s *Store, bucket []byte, id string, rec T) error {
	if id == "" {
		return errors.New("store: empty id")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("store: marshal: %w", err)
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(id), raw)
	})
}

func get[T any](s *Store, bucket []byte, id string) (T, error) {
	var rec T
	err := s.db.View(func(tx *bbolt.Tx) error {
		raw := tx.Bucket(bucket).Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		return json.Unmarshal(raw, &rec)
	})
	return rec, err
}

func list[T any](s *Store, bucket []byte) ([]T, error) {
	var out []T
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucket).ForEach(func(k, v []byte) error {
			if v == nil { // nested bucket, not a record
				return nil
			}
			var rec T
			if err := json.Unmarshal(v, &rec); err != nil {
				return fmt.Errorf("store: unmarshal %s/%s: %w", bucket, k, err)
			}
			out = append(out, rec)
			return nil
		})
	})
	return out, err
}

func del(s *Store, bucket []byte, id string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucket).Delete([]byte(id))
	})
}

// --- profiles --------------------------------------------------------------

func (s *Store) PutProfile(p Profile) error         { return put(s, bucketProfiles, p.ID, p) }
func (s *Store) Profile(id string) (Profile, error) { return get[Profile](s, bucketProfiles, id) }
func (s *Store) Profiles() ([]Profile, error)       { return list[Profile](s, bucketProfiles) }
func (s *Store) DeleteProfile(id string) error      { return del(s, bucketProfiles, id) }

// --- vaults ----------------------------------------------------------------

func (s *Store) PutVault(v Vault) error         { return put(s, bucketVaults, v.ID, v) }
func (s *Store) Vault(id string) (Vault, error) { return get[Vault](s, bucketVaults, id) }
func (s *Store) Vaults() ([]Vault, error)       { return list[Vault](s, bucketVaults) }

// DeleteVault removes the vault record along with its snapshot and activity
// history, so a recreated vault of the same name does not inherit stale charts.
func (s *Store) DeleteVault(id string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.Bucket(bucketVaults).Delete([]byte(id)); err != nil {
			return err
		}
		// A move in flight for a vault that no longer exists has nothing left to
		// finish, and leaving the record would make the next vault of the same
		// id inherit it.
		if err := tx.Bucket(bucketMigrations).Delete([]byte(id)); err != nil {
			return err
		}
		for _, parent := range [][]byte{bucketSnapshots, bucketActivity} {
			b := tx.Bucket(parent)
			if b.Bucket([]byte(id)) == nil {
				continue
			}
			if err := b.DeleteBucket([]byte(id)); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- migrations ------------------------------------------------------------

func (s *Store) PutMigration(m Migration) error { return put(s, bucketMigrations, m.VaultID, m) }
func (s *Store) Migration(vaultID string) (Migration, error) {
	return get[Migration](s, bucketMigrations, vaultID)
}
func (s *Store) DeleteMigration(vaultID string) error { return del(s, bucketMigrations, vaultID) }

// --- snapshots -------------------------------------------------------------

// AppendSnapshot records one poll. Keys are big-endian timestamps so bbolt's
// cursor iterates chronologically.
func (s *Store) AppendSnapshot(snap Snapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("store: marshal snapshot: %w", err)
	}
	var key [8]byte
	binary.BigEndian.PutUint64(key[:], uint64(snap.At.UTC().UnixMilli()))

	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.Bucket(bucketSnapshots).CreateBucketIfNotExists([]byte(snap.VaultID))
		if err != nil {
			return err
		}
		return b.Put(key[:], raw)
	})
}

// Snapshots returns a vault's snapshots from since onwards, oldest first.
func (s *Store) Snapshots(vaultID string, since time.Time) ([]Snapshot, error) {
	var out []Snapshot
	var start [8]byte
	binary.BigEndian.PutUint64(start[:], uint64(since.UTC().UnixMilli()))

	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSnapshots).Bucket([]byte(vaultID))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(start[:]); k != nil; k, v = c.Next() {
			var snap Snapshot
			if err := json.Unmarshal(v, &snap); err != nil {
				return fmt.Errorf("store: unmarshal snapshot: %w", err)
			}
			out = append(out, snap)
		}
		return nil
	})
	return out, err
}

// LatestSnapshot returns the most recent snapshot for a vault.
func (s *Store) LatestSnapshot(vaultID string) (Snapshot, error) {
	var snap Snapshot
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSnapshots).Bucket([]byte(vaultID))
		if b == nil {
			return ErrNotFound
		}
		_, v := b.Cursor().Last()
		if v == nil {
			return ErrNotFound
		}
		return json.Unmarshal(v, &snap)
	})
	return snap, err
}

// PruneSnapshots drops snapshots older than before.
func (s *Store) PruneSnapshots(vaultID string, before time.Time) error {
	var cutoff [8]byte
	binary.BigEndian.PutUint64(cutoff[:], uint64(before.UTC().UnixMilli()))

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketSnapshots).Bucket([]byte(vaultID))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil && string(k) < string(cutoff[:]); k, _ = c.Next() {
			if err := c.Delete(); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- activity --------------------------------------------------------------

// DayKey formats a time as the UTC day key used by the activity buckets.
func DayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// AddActivity accumulates writes into a vault's day bucket.
//
// Counts saturate at math.MaxUint32 rather than wrapping: a wrapped counter
// would render as a suspiciously quiet day, which is worse than a capped one.
func (s *Store) AddActivity(vaultID string, day string, writes uint32) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.Bucket(bucketActivity).CreateBucketIfNotExists([]byte(vaultID))
		if err != nil {
			return err
		}
		total := uint64(writes)
		if prev := b.Get([]byte(day)); len(prev) == 4 {
			total += uint64(binary.BigEndian.Uint32(prev))
		}
		if total > 0xFFFFFFFF {
			total = 0xFFFFFFFF
		}
		var buf [4]byte
		binary.BigEndian.PutUint32(buf[:], uint32(total))
		return b.Put([]byte(day), buf[:])
	})
}

// Activity returns a vault's daily write counts from day `from` onwards,
// oldest first. Days with no writes are absent rather than zero-filled; the UI
// fills the calendar grid.
func (s *Store) Activity(vaultID string, from time.Time) ([]ActivityDay, error) {
	start := []byte(DayKey(from))
	var out []ActivityDay

	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketActivity).Bucket([]byte(vaultID))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(start); k != nil; k, v = c.Next() {
			if len(v) != 4 {
				continue
			}
			out = append(out, ActivityDay{Day: string(k), Writes: binary.BigEndian.Uint32(v)})
		}
		return nil
	})
	return out, err
}

// PruneActivity drops day buckets older than before.
func (s *Store) PruneActivity(vaultID string, before time.Time) error {
	cutoff := DayKey(before)
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketActivity).Bucket([]byte(vaultID))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil && string(k) < cutoff; k, _ = c.Next() {
			if err := c.Delete(); err != nil {
				return err
			}
		}
		return nil
	})
}
