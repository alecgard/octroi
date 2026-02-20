package agent

import (
	"testing"
	"time"
)

func TestEncodeCursor(t *testing.T) {
	ts := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	id := "550e8400-e29b-41d4-a716-446655440000"

	cursor := encodeCursor(ts, id)
	if cursor == "" {
		t.Fatal("expected non-empty cursor")
	}

	gotTime, gotID, err := decodeCursor(cursor)
	if err != nil {
		t.Fatalf("unexpected error decoding cursor: %v", err)
	}
	if !gotTime.Equal(ts) {
		t.Errorf("time mismatch: got %v, want %v", gotTime, ts)
	}
	if gotID != id {
		t.Errorf("id mismatch: got %q, want %q", gotID, id)
	}
}

func TestDecodeCursorInvalidBase64(t *testing.T) {
	_, _, err := decodeCursor("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDecodeCursorInvalidFormat(t *testing.T) {
	// Valid base64 but missing the pipe separator.
	_, _, err := decodeCursor("bm9waXBl") // "nopipe"
	if err == nil {
		t.Fatal("expected error for missing separator")
	}
}

func TestDecodeCursorInvalidTime(t *testing.T) {
	// "bad-time|some-id" in base64.
	_, _, err := decodeCursor("YmFkLXRpbWV8c29tZS1pZA==")
	if err == nil {
		t.Fatal("expected error for invalid time")
	}
}

func TestEncodeCursorRoundTripNano(t *testing.T) {
	ts := time.Date(2024, 1, 2, 3, 4, 5, 123456789, time.UTC)
	id := "test-id"

	cursor := encodeCursor(ts, id)
	gotTime, gotID, err := decodeCursor(cursor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gotTime.Equal(ts) {
		t.Errorf("nanosecond precision lost: got %v, want %v", gotTime, ts)
	}
	if gotID != id {
		t.Errorf("id mismatch: got %q, want %q", gotID, id)
	}
}
