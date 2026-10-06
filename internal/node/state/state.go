// Package state persists what the node agent has applied, so a node keeps serving its last
// known config when the panel is unreachable (docs/ARCHITECTURE.md §4.2).
package state

import (
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketState = []byte("state")
	keyCurrent  = []byte("current")
)

// User is a client of an inbound.
type User struct {
	Email string `json:"email"`
	ID    string `json:"id"`
}

// Inbound lists the users of one inbound.
type Inbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Flow     string `json:"flow"`
	Users    []User `json:"users"`
}

// State is the applied desired state.
type State struct {
	Revision int64     `json:"revision"`
	Hash     string    `json:"hash"`
	Config   []byte    `json:"config"` // Xray config without clients
	Inbounds []Inbound `json:"inbounds"`
	Caddy    []byte    `json:"caddy,omitempty"`
}

// Inbound returns the inbound with tag, or nil.
func (s *State) Inbound(tag string) *Inbound {
	for i := range s.Inbounds {
		if s.Inbounds[i].Tag == tag {
			return &s.Inbounds[i]
		}
	}
	return nil
}

// Clone deep-copies the state.
func (s *State) Clone() *State {
	b, _ := json.Marshal(s)
	var out State
	_ = json.Unmarshal(b, &out)
	return &out
}

// Store wraps the bbolt file.
type Store struct {
	db *bolt.DB
}

// Open opens or creates the state file.
func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketState)
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the file.
func (s *Store) Close() error { return s.db.Close() }

// Load returns the saved state or nil.
func (s *Store) Load() (*State, error) {
	var st *State
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketState).Get(keyCurrent)
		if raw == nil {
			return nil
		}
		st = &State{}
		return json.Unmarshal(raw, st)
	})
	return st, err
}

// Save stores the state.
func (s *Store) Save(st *State) error {
	if st == nil {
		return errors.New("nil state")
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(bucketState).Put(keyCurrent, raw) })
}
