// Package domain is pure Go: no SDK, HTTP or database imports.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Typed domain errors; driving adapters map them to status codes.
var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// ItemName is a value object: valid once constructed.
type ItemName struct{ value string }

func NewItemName(raw string) (ItemName, error) {
	v := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(v); n == 0 || n > 100 {
		return ItemName{}, fmt.Errorf("%w: name must be 1-100 characters", ErrInvalid)
	}
	return ItemName{v}, nil
}

func (n ItemName) String() string { return n.value }

type Item struct {
	ID        string
	Name      ItemName
	CreatedAt time.Time
}
