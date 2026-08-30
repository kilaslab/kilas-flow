package workflow

import (
	"fmt"

	"github.com/google/uuid"
)

// NewID returns a time-ordered, prefixed UUIDv7 for a KilasFlow resource.
func NewID(prefix string) (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + value.String(), nil
}
