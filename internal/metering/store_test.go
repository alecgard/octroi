package metering

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockStore records all batches that were inserted.
type mockStore struct {
	mu       sync.Mutex
	batches  [][]Transaction
	insertFn func(ctx context.Context, txns []Transaction) error
}

func (m *mockStore) BatchInsert(ctx context.Context, txns []Transaction) error {
	if m.insertFn != nil {
		return m.insertFn(ctx, txns)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]Transaction, len(txns))
	copy(cp, txns)
	m.batches = append(m.batches, cp)
	return nil
}

func (m *mockStore) totalInserted() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.batches {
		n += len(b)
	}
	return n
}

func sampleTx(method string) Transaction {
	return Transaction{
		AgentID:    "agent-1",
		ToolID:     "tool-1",
		Timestamp:  time.Now(),
		Method:     method,
		Path:       "/test",
		StatusCode: 200,
		LatencyMs:  42,
		Success:    true,
	}
}

func TestCollector_RecordAddsToBuffer(t *testing.T) {
	ms := &mockStore{}
	c := NewCollector(ms, 100, time.Hour) // large batch size, long interval

	c.Record(sampleTx("GET"))
	c.Record(sampleTx("POST"))

	c.mu.Lock()
	bufLen := len(c.buffer)
	c.mu.Unlock()

	if bufLen != 2 {
		t.Fatalf("expected buffer length 2, got %d", bufLen)
	}

	if ms.totalInserted() != 0 {
		t.Fatalf("expected 0 inserted before flush, got %d", ms.totalInserted())
	}
}

func TestCollector_FlushOnBatchSize(t *testing.T) {
	tests := []struct {
		name      string
		batchSize int
		records   int
		wantFlush int // number of total transactions flushed
	}{
		{
			name:      "exact batch size triggers flush",
			batchSize: 3,
			records:   3,
			wantFlush: 3,
		},
		{
			name:      "under batch size does not flush",
			batchSize: 5,
			records:   3,
			wantFlush: 0,
		},
		{
			name:      "double batch size triggers two flushes",
			batchSize: 2,
			records:   4,
			wantFlush: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := &mockStore{}
			c := NewCollector(ms, tt.batchSize, time.Hour)

			for i := 0; i < tt.records; i++ {
				c.Record(sampleTx("GET"))
			}

			// Allow any concurrent flush goroutine to complete.
			time.Sleep(50 * time.Millisecond)

			got := ms.totalInserted()
			if got != tt.wantFlush {
				t.Errorf("expected %d flushed transactions, got %d", tt.wantFlush, got)
			}
		})
	}
}

func TestCollector_StopDoFinalFlush(t *testing.T) {
	ms := &mockStore{}
	c := NewCollector(ms, 100, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go c.Start(ctx)

	c.Record(sampleTx("GET"))
	c.Record(sampleTx("POST"))
	c.Record(sampleTx("DELETE"))

	// Stop triggers a final flush.
	c.Stop()

	// Give the goroutine a moment to process the final flush.
	time.Sleep(100 * time.Millisecond)

	got := ms.totalInserted()
	if got != 3 {
		t.Fatalf("expected 3 transactions after Stop, got %d", got)
	}
}

func TestCollector_TimerFlush(t *testing.T) {
	ms := &mockStore{}
	c := NewCollector(ms, 100, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go c.Start(ctx)

	c.Record(sampleTx("GET"))

	// Wait for the flush interval to fire.
	time.Sleep(200 * time.Millisecond)

	got := ms.totalInserted()
	if got != 1 {
		t.Fatalf("expected 1 transaction after timer flush, got %d", got)
	}

	c.Stop()
}

func TestCollector_ConcurrentRecords(t *testing.T) {
	ms := &mockStore{}
	c := NewCollector(ms, 10, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go c.Start(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Record(sampleTx("GET"))
		}()
	}
	wg.Wait()

	c.Stop()
	time.Sleep(100 * time.Millisecond)

	got := ms.totalInserted()
	if got != 50 {
		t.Fatalf("expected 50 transactions, got %d", got)
	}
}
