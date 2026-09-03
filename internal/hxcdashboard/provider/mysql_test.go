package provider

import (
	"strings"
	"testing"
	"time"
)

func TestCurrentQueryIsKeysetBatchedAndArgumentComplete(t *testing.T) {
	if strings.Contains(currentBatchSQL, "10001") || !strings.Contains(currentBatchSQL, "id>?") || !strings.Contains(currentBatchSQL, "LIMIT ?") {
		t.Fatal("query must use an uncapped keyset batch")
	}
	if got, want := strings.Count(currentBatchSQL, "?"), len(batchArgs("", time.Now())); got != want {
		t.Fatalf("placeholders=%d args=%d", got, want)
	}
}
