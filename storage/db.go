package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/argon2"
)

var (
	ErrNotFound      = errors.New("key not found")
	ErrBucketMissing = errors.New("bucket not found")
	ErrDecryptFailed = errors.New("decryption failed")
	ErrKeyExists     = errors.New("key already exists")
)

// Standard bucket names
var (
	BucketUsers        = []byte("users")
	BucketSessions     = []byte("sessions")
	BucketMounts       = []byte("mounts")
	BucketSettings     = []byte("settings")
	BucketTasks        = []byte("tasks")
	BucketRemotes      = []byte("remotes")
	BucketMeta         = []byte("meta")
	BucketThumbnails   = []byte("thumbnails")
	BucketCertificates = []byte("certificates")
	BucketTelegram     = []byte("telegram")
	BucketTransfers    = []byte("transfers")
	BucketTransferLog  = []byte("transfer_log")
	BucketHistory          = []byte("history")
	BucketClipboardHistory = []byte("clipboard_history")
	BucketVFSTree          = []byte("vfs_tree")
	BucketVFSHeaders       = []byte("vfs_headers")
	BucketMediaCache       = []byte("media_cache")
	BucketEditDrafts       = []byte("edit_drafts")
)

// DB manages an encrypted BBolt embedded database.
type DB struct {
	db     *bolt.DB
	key    []byte // 32-byte AES-256 key
	mu     sync.RWMutex
	closed bool
}

// Config provides setup parameters for the secure DB.
type Config struct {
	Path       string
	Passphrase string
	Salt       []byte // 16-byte salt; generated and persisted if empty
}

// Open initializes or creates the secure database.
func Open(cfg Config) (*DB, error) {
	if cfg.Path == "" {
		return nil, errors.New("database path required")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0700); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	boltDB, err := bolt.Open(cfg.Path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open bbolt db: %w", err)
	}

	// Initialize buckets and retrieve/store master salt
	var salt []byte
	err = boltDB.Update(func(tx *bolt.Tx) error {
		buckets := [][]byte{
			BucketUsers,
			BucketSessions,
			BucketMounts,
			BucketSettings,
			BucketTasks,
			BucketRemotes,
			BucketMeta,
			BucketThumbnails,
			BucketCertificates,
			BucketTelegram,
			BucketTransfers,
			BucketTransferLog,
			BucketHistory,
			BucketClipboardHistory,
			BucketVFSTree,
			BucketVFSHeaders,
			BucketMediaCache,
			BucketEditDrafts,
		}
		for _, b := range buckets {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}

		settingsBucket := tx.Bucket(BucketSettings)
		existingSalt := settingsBucket.Get([]byte("master_salt"))
		if existingSalt != nil && len(existingSalt) == 16 {
			salt = make([]byte, 16)
			copy(salt, existingSalt)
		} else {
			if len(cfg.Salt) == 16 {
				salt = cfg.Salt
			} else {
				salt = make([]byte, 16)
				if _, err := io.ReadFull(rand.Reader, salt); err != nil {
					return err
				}
			}
			if err := settingsBucket.Put([]byte("master_salt"), salt); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		_ = boltDB.Close()
		return nil, fmt.Errorf("db initialization failed: %w", err)
	}

	pass := cfg.Passphrase
	if pass == "" {
		pass = "freebox-default-secure-passphrase"
	}

	// Derive 32-byte key with Argon2id
	derivedKey := argon2.IDKey([]byte(pass), salt, 1, 64*1024, 4, 32)

	return &DB{
		db:  boltDB,
		key: derivedKey,
	}, nil
}

// Close closes the underlying BBolt database.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.db.Close()
}

// Encrypt encrypts plaintext using AES-GCM with a random nonce.
func (d *DB) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(d.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts AES-GCM ciphertext.
func (d *DB) Decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(d.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrDecryptFailed
	}
	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}

// Put writes an unencrypted value to the given bucket.
func (d *DB) Put(bucket []byte, key string, value []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return errors.New("db is closed")
	}

	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return ErrBucketMissing
		}
		return b.Put([]byte(key), value)
	})
}

// Get reads an unencrypted value from the given bucket.
func (d *DB) Get(bucket []byte, key string) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return nil, errors.New("db is closed")
	}

	var val []byte
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return ErrBucketMissing
		}
		v := b.Get([]byte(key))
		if v == nil {
			return ErrNotFound
		}
		val = make([]byte, len(v))
		copy(val, v)
		return nil
	})
	return val, err
}

// PutEncrypted encrypts and writes value to bucket.
func (d *DB) PutEncrypted(bucket []byte, key string, plaintext []byte) error {
	cipherData, err := d.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encryption error: %w", err)
	}
	return d.Put(bucket, key, cipherData)
}

// PutEncryptedIfAbsent inserts an encrypted record exactly once. It is used by
// append-only journals, where changing a completed event would hide recovery
// history. The caller supplies a monotonic, unique key.
func (d *DB) PutEncryptedIfAbsent(bucket []byte, key string, plaintext []byte) error {
	cipherData, err := d.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encryption error: %w", err)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return errors.New("db is closed")
	}
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return ErrBucketMissing
		}
		if b.Get([]byte(key)) != nil {
			return ErrKeyExists
		}
		return b.Put([]byte(key), cipherData)
	})
}

// GetDecrypted reads and decrypts ciphertext from bucket.
func (d *DB) GetDecrypted(bucket []byte, key string) ([]byte, error) {
	cipherData, err := d.Get(bucket, key)
	if err != nil {
		return nil, err
	}
	return d.Decrypt(cipherData)
}

// Delete removes key from bucket.
func (d *DB) Delete(bucket []byte, key string) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return errors.New("db is closed")
	}

	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return ErrBucketMissing
		}
		return b.Delete([]byte(key))
	})
}

// List returns all raw key-value pairs in a bucket.
func (d *DB) List(bucket []byte) (map[string][]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return nil, errors.New("db is closed")
	}

	res := make(map[string][]byte)
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return ErrBucketMissing
		}
		return b.ForEach(func(k, v []byte) error {
			valCopy := make([]byte, len(v))
			copy(valCopy, v)
			res[string(k)] = valCopy
			return nil
		})
	})
	return res, err
}

// ListDecrypted returns all decrypted key-value pairs in a bucket.
func (d *DB) ListDecrypted(bucket []byte) (map[string][]byte, error) {
	raw, err := d.List(bucket)
	if err != nil {
		return nil, err
	}

	res := make(map[string][]byte, len(raw))
	for k, v := range raw {
		plain, err := d.Decrypt(v)
		if err != nil {
			return nil, fmt.Errorf("failed decrypting key %s: %w", k, err)
		}
		res[k] = plain
	}
	return res, nil
}
