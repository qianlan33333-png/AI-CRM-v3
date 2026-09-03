package port

import (
	"errors"
	"testing"
)

func TestProviderCallAttemptedIsConservative(t *testing.T) {
	if ProviderCallAttempted(WrapProviderWriteError(errors.New("token"), false)) {
		t.Fatal("token acquisition failure was classified as provider call")
	}
	if !ProviderCallAttempted(WrapProviderWriteError(errors.New("timeout"), true)) {
		t.Fatal("provider timeout was not classified as attempted")
	}
	if !ProviderCallAttempted(errors.New("unclassified adapter failure")) {
		t.Fatal("unclassified failure must fail safe as attempted")
	}
}
