package blocking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/bloom"
	"github.com/bitmagnet-io/bitmagnet/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Manager interface {
	Filter(ctx context.Context, hashes []protocol.ID) ([]protocol.ID, error)
	Block(ctx context.Context, hashes []protocol.ID, flush bool) error
	Flush(ctx context.Context) error
}

type manager struct {
	// mutex guards buffer, filter and lastFlushedAt. The read path never holds it
	// across the database; flush still does, because persisting is a write.
	mutex sync.Mutex
	// reloadMutex admits one reloader at a time. A caller that finds it taken
	// already has a usable filter, and serves that rather than queueing.
	reloadMutex   sync.Mutex
	pool          *pgxpool.Pool
	buffer        map[protocol.ID]struct{}
	filter        *bloom.StableBloomFilter
	maxBufferSize int
	lastFlushedAt time.Time
	maxFlushWait  time.Duration
}

func (m *manager) Filter(ctx context.Context, hashes []protocol.ID) ([]protocol.ID, error) {
	if err := m.refreshForRead(ctx); err != nil {
		return nil, err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	filtered := make([]protocol.ID, 0, len(hashes))

	for _, hash := range hashes {
		if _, ok := m.buffer[hash]; ok {
			continue
		}

		if m.filter.Test(hash[:]) {
			continue
		}

		filtered = append(filtered, hash)
	}

	return filtered, nil
}

func (m *manager) Block(ctx context.Context, hashes []protocol.ID, flush bool) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for _, hash := range hashes {
		m.buffer[hash] = struct{}{}
	}

	if flush || m.shouldFlush() {
		if refreshErr := m.refresh(ctx); refreshErr != nil {
			return refreshErr
		}
	}

	return nil
}

func (m *manager) Flush(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if len(m.buffer) == 0 {
		return nil
	}

	return m.flush(ctx)
}

const blockedTorrentsBloomFilterKey = "blocked_torrents"

// refresh brings the in-memory filter up to date. It does two jobs that look like
// one: persisting buffered hashes, and picking up blocks made by another process.
// Only the first needs to write. Callers hold the mutex, so it assigns the reloaded
// filter directly; the read path goes through refreshForRead instead.
func (m *manager) refresh(ctx context.Context) error {
	if len(m.buffer) > 0 {
		return m.flush(ctx)
	}

	bf, err := m.readFilter(ctx)
	if err != nil {
		return err
	}

	m.filter = bf
	m.lastFlushedAt = time.Now()

	return nil
}

// refreshForRead brings the filter up to date for a read without holding the mutex
// across the transfer. readFilter allocates and reads a 25 MB filter, and Filter runs
// on the crawler's triage path at thousands of hashes per second, so doing that under
// the mutex stalls the whole pipeline every maxFlushWait.
//
// A caller with no filter at all has nothing to answer from and waits for one. A
// caller whose filter is merely overdue serves the filter it has: maxFlushWait is
// already the statement of how stale that may be, and queueing behind the read is
// the cost this exists to avoid.
func (m *manager) refreshForRead(ctx context.Context) error {
	m.mutex.Lock()
	cold := m.filter == nil
	due := cold || m.shouldFlush()
	buffered := len(m.buffer) > 0
	m.mutex.Unlock()

	if !due {
		return nil
	}

	// Buffered hashes make this a write, which stays serialised with the rest of
	// the manager.
	if buffered {
		m.mutex.Lock()
		defer m.mutex.Unlock()

		if len(m.buffer) == 0 {
			return nil
		}

		return m.flush(ctx)
	}

	if cold {
		m.reloadMutex.Lock()
	} else if !m.reloadMutex.TryLock() {
		return nil
	}

	defer m.reloadMutex.Unlock()

	// Whoever held reloadMutex before us may have just done this work.
	m.mutex.Lock()
	done := m.filter != nil && !m.shouldFlush()
	m.mutex.Unlock()

	if done {
		return nil
	}

	bf, err := m.readFilter(ctx)
	if err != nil {
		return err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.filter = bf
	m.lastFlushedAt = time.Now()

	return nil
}

// readFilter returns the stored filter without writing and without touching manager
// state, so it is safe to call with no lock held. A missing or null OID means nothing
// has been blocked yet, which is not an error: the empty filter it starts with is the
// right answer, and flush creates the large object when there is finally something to
// store.
func (m *manager) readFilter(ctx context.Context) (*bloom.StableBloomFilter, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	bf := bloom.NewDefaultStableBloomFilter()

	var nullOid sql.NullInt32

	err = tx.QueryRow(ctx, "SELECT oid FROM bloom_filters WHERE key = $1", blockedTorrentsBloomFilterKey).
		Scan(&nullOid)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to get bloom filter object ID: %w", err)
	}

	if err == nil && nullOid.Valid {
		lobs := tx.LargeObjects()

		obj, openErr := lobs.Open(ctx, uint32(nullOid.Int32), pgx.LargeObjectModeRead)
		if openErr != nil {
			return nil, fmt.Errorf("failed to open large object for reading: %w", openErr)
		}

		_, readErr := bf.ReadFrom(obj)
		obj.Close()

		if readErr != nil {
			return nil, fmt.Errorf("failed to read current bloom filter: %w", readErr)
		}
	}

	if commitErr := tx.Commit(ctx); commitErr != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", commitErr)
	}

	return bf, nil
}

func (m *manager) flush(ctx context.Context) error {
	hashes := slices.Collect(maps.Keys(m.buffer))

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{
		AccessMode: pgx.ReadWrite,
	})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if len(hashes) > 0 {
		_, err = tx.Exec(ctx, "DELETE FROM torrents WHERE info_hash = any($1)", hashes)
		if err != nil {
			return fmt.Errorf("failed to delete from torrents table: %w", err)
		}
	}

	bf := bloom.NewDefaultStableBloomFilter()

	lobs := tx.LargeObjects()

	found := false

	var oid uint32

	var nullOid sql.NullInt32

	err = tx.QueryRow(ctx, "SELECT oid FROM bloom_filters WHERE key = $1", blockedTorrentsBloomFilterKey).
		Scan(&nullOid)
	if err == nil {
		found = true

		if nullOid.Valid {
			oid = uint32(nullOid.Int32)

			obj, err := lobs.Open(ctx, oid, pgx.LargeObjectModeRead)
			if err != nil {
				return fmt.Errorf("failed to open large object for reading: %w", err)
			}

			_, err = bf.ReadFrom(obj)
			obj.Close()

			if err != nil {
				return fmt.Errorf("failed to read current bloom filter: %w", err)
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to get bloom filter object ID: %w", err)
	}

	if oid == 0 {
		// Create a new Large Object.
		// We pass 0, so the DB can pick an available oid for us.
		oid, err = lobs.Create(ctx, 0)
		if err != nil {
			return fmt.Errorf("failed to create large object: %w", err)
		}
	}

	for _, hash := range hashes {
		bf.Add(hash[:])
	}

	obj, err := lobs.Open(ctx, oid, pgx.LargeObjectModeWrite)
	if err != nil {
		return fmt.Errorf("failed to open large object for writing: %w", err)
	}

	_, err = bf.WriteTo(obj)
	if err != nil {
		return fmt.Errorf("failed to write to large object: %w", err)
	}

	now := time.Now()
	if !found {
		_, err = tx.Exec(ctx,
			"INSERT INTO bloom_filters (key, oid, created_at, updated_at) VALUES ($1, $2, $3, $4)",
			blockedTorrentsBloomFilterKey, oid, now, now)
		if err != nil {
			return fmt.Errorf("failed to save new bloom filter record: %w", err)
		}
	} else if !nullOid.Valid {
		_, err = tx.Exec(ctx,
			"UPDATE bloom_filters SET oid = $1, updated_at = $2 WHERE key = $3",
			oid, now, blockedTorrentsBloomFilterKey)
		if err != nil {
			return fmt.Errorf("failed to update bloom filter record: %w", err)
		}
	}

	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	m.buffer = make(map[protocol.ID]struct{})
	m.filter = bf
	m.lastFlushedAt = now

	return nil
}

func (m *manager) shouldFlush() bool {
	return len(m.buffer) >= m.maxBufferSize || time.Since(m.lastFlushedAt) >= m.maxFlushWait
}
