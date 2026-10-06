package state

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/vyto4ka/vpn/internal/proto/vpn/node/v1"
)

var (
	bucketStats = []byte("stats")
	bucketMeta  = []byte("meta")
	keyEpoch    = []byte("stats_epoch")
	keySeq      = []byte("stats_seq")
)

// MaxQueuedBatches caps the stats queue (~2.3 days at one batch per 10s); the oldest are dropped.
const MaxQueuedBatches = 20000

func (s *Store) ensureStats(tx *bolt.Tx) (*bolt.Bucket, *bolt.Bucket, error) {
	st, err := tx.CreateBucketIfNotExists(bucketStats)
	if err != nil {
		return nil, nil, err
	}
	meta, err := tx.CreateBucketIfNotExists(bucketMeta)
	if err != nil {
		return nil, nil, err
	}
	if meta.Get(keyEpoch) == nil {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		if err := meta.Put(keyEpoch, []byte(hex.EncodeToString(b))); err != nil {
			return nil, nil, err
		}
	}
	return st, meta, nil
}

// Enqueue stamps a batch with the queue epoch and the next sequence number and stores it.
func (s *Store) Enqueue(b *nodev1.StatsBatch) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		st, meta, err := s.ensureStats(tx)
		if err != nil {
			return err
		}
		var seq uint64
		if v := meta.Get(keySeq); v != nil {
			seq = binary.BigEndian.Uint64(v)
		}
		seq++
		b.Epoch, b.Seq = string(meta.Get(keyEpoch)), seq
		raw, err := proto.Marshal(b)
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, seq)
		if err := st.Put(key, raw); err != nil {
			return err
		}
		if err := meta.Put(keySeq, key); err != nil {
			return err
		}
		for st.Stats().KeyN >= MaxQueuedBatches {
			k, _ := st.Cursor().First()
			if k == nil {
				break
			}
			if err := st.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// Pending returns up to limit queued batches with seq > after, oldest first.
func (s *Store) Pending(after uint64, limit int) ([]*nodev1.StatsBatch, error) {
	var out []*nodev1.StatsBatch
	err := s.db.View(func(tx *bolt.Tx) error {
		st := tx.Bucket(bucketStats)
		if st == nil {
			return nil
		}
		start := make([]byte, 8)
		binary.BigEndian.PutUint64(start, after+1)
		c := st.Cursor()
		for k, v := c.Seek(start); k != nil && len(out) < limit; k, v = c.Next() {
			b := &nodev1.StatsBatch{}
			if err := proto.Unmarshal(v, b); err != nil {
				return err
			}
			out = append(out, b)
		}
		return nil
	})
	return out, err
}

// AckStats removes acknowledged batches (seq <= the acked one) of the current epoch.
func (s *Store) AckStats(epoch string, seq uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		st, meta, err := s.ensureStats(tx)
		if err != nil {
			return err
		}
		if string(meta.Get(keyEpoch)) != epoch {
			return nil
		}
		c := st.Cursor()
		for k, _ := c.First(); k != nil && binary.BigEndian.Uint64(k) <= seq; k, _ = c.First() {
			if err := st.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// QueueLen returns how many batches wait for an ack.
func (s *Store) QueueLen() int {
	n := 0
	_ = s.db.View(func(tx *bolt.Tx) error {
		if st := tx.Bucket(bucketStats); st != nil {
			n = st.Stats().KeyN
		}
		return nil
	})
	return n
}
