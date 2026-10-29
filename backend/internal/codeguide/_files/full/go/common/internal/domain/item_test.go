package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNewItemName(t *testing.T) {
	if n, err := NewItemName("  pen "); err != nil || n.String() != "pen" {
		t.Fatalf("valid name: %q %v", n, err)
	}
	for _, raw := range []string{"", "   ", strings.Repeat("x", 101)} {
		if _, err := NewItemName(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", raw, err)
		}
	}
}
