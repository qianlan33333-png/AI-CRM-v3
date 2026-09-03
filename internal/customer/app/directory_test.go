package app

import (
	"context"
	"errors"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type fakeDirectoryStore struct{ calls []Query }

func (store *fakeDirectoryStore) List(_ context.Context, query Query) (PageData, error) {
	store.calls = append(store.calls, query)
	items := []Item{{CustomerID: 1, UpdatedAt: query.Watermark.Add(-time.Minute)}, {CustomerID: 2, UpdatedAt: query.Watermark.Add(-2 * time.Minute)}, {CustomerID: 3, UpdatedAt: query.Watermark.Add(-3 * time.Minute)}}
	if !query.AfterAt.IsZero() {
		items = []Item{{CustomerID: 4, UpdatedAt: query.AfterAt.Add(-time.Minute)}}
	}
	return PageData{Items: items, Count: 4}, nil
}
func (*fakeDirectoryStore) Detail(context.Context, customerdomain.CustomerID) (Detail, error) {
	return Detail{}, ErrNotFound
}

func TestDirectoryCursorBindsWatermarkSortAndFilters(t *testing.T) {
	store := &fakeDirectoryStore{}
	now := time.Date(2026, 9, 3, 2, 30, 0, 0, time.UTC)
	directory := Directory{Store: store, Now: func() time.Time { return now }, SigningKey: []byte("0123456789abcdef0123456789abcdef")}
	first, err := directory.List(context.Background(), ListRequest{Limit: 2, Filters: Filters{Keyword: "Alice", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" || len(first.Items) != 2 || !first.Watermark.Equal(now) {
		t.Fatalf("first=%+v", first)
	}
	second, err := directory.List(context.Background(), ListRequest{Limit: 2, Cursor: first.NextCursor, Filters: Filters{Keyword: "Alice", Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || !store.calls[1].Watermark.Equal(now) || store.calls[1].AfterID != 2 {
		t.Fatalf("second=%+v query=%+v", second, store.calls[1])
	}
	_, err = directory.List(context.Background(), ListRequest{Limit: 2, Cursor: first.NextCursor, Filters: Filters{Keyword: "Bob", Status: "active"}})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("filter drift err=%v", err)
	}
	tampered := first.NextCursor[:len(first.NextCursor)-1] + "x"
	if _, err = directory.List(context.Background(), ListRequest{Limit: 2, Cursor: tampered, Filters: Filters{Keyword: "Alice", Status: "active"}}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tamper err=%v", err)
	}
}

func TestDirectoryRejectsInvalidBounds(t *testing.T) {
	store := &fakeDirectoryStore{}
	directory := Directory{Store: store, SigningKey: []byte("0123456789abcdef0123456789abcdef")}
	for _, request := range []ListRequest{{Limit: 201}, {Filters: Filters{Status: "unknown"}}, {Filters: Filters{ActivationStatus: "closed"}}} {
		if _, err := directory.List(context.Background(), request); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("request=%+v err=%v", request, err)
		}
	}
}
