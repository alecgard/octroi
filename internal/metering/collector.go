package metering

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// BatchInserter is the interface used by Collector to persist transactions.
// It exists to allow testing without a real database.
type BatchInserter interface {
	BatchInsert(ctx context.Context, txns []Transaction) error
}

// Collector buffers transactions in memory and periodically flushes them to the
// store in batches. It is safe for concurrent use.
type Collector struct {
	store         BatchInserter
	buffer        []Transaction
	mu            sync.Mutex
	batchSize     int
	flushInterval time.Duration
	done          chan struct{}
}

// NewCollector creates a new Collector that flushes to the given store when the
// buffer reaches batchSize or every flushInterval, whichever comes first.
func NewCollector(store BatchInserter, batchSize int, flushInterval time.Duration) *Collector {
	return &Collector{
		store:         store,
		buffer:        make([]Transaction, 0, batchSize),
		batchSize:     batchSize,
		flushInterval: flushInterval,
		done:          make(chan struct{}),
	}
}

// Start begins a background goroutine that flushes buffered transactions on a
// timer. It blocks until Stop is called or the context is cancelled.
func (c *Collector) Start(ctx context.Context) {
	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.flush()
		case <-ctx.Done():
			c.flush()
			return
		case <-c.done:
			c.flush()
			return
		}
	}
}

// Record adds a transaction to the buffer. If the buffer reaches batchSize,
// a flush is triggered immediately.
func (c *Collector) Record(tx Transaction) {
	c.mu.Lock()
	c.buffer = append(c.buffer, tx)
	shouldFlush := len(c.buffer) >= c.batchSize
	c.mu.Unlock()

	if shouldFlush {
		c.flush()
	}
}

// flush drains all buffered transactions and writes them to the store. It logs
// errors rather than returning them so callers are not blocked.
func (c *Collector) flush() {
	c.mu.Lock()
	if len(c.buffer) == 0 {
		c.mu.Unlock()
		return
	}
	batch := c.buffer
	c.buffer = make([]Transaction, 0, c.batchSize)
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.store.BatchInsert(ctx, batch); err != nil {
		slog.Error("failed to flush metering transactions", "count", len(batch), "error", err)
	}
}

// Stop signals the background goroutine to exit and performs a final flush.
func (c *Collector) Stop() {
	close(c.done)
}
