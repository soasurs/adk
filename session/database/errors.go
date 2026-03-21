package database

import (
	"errors"
	"fmt"
)

// ErrInvalidTableName indicates that a configured table name is not a valid
// SQL identifier.
var ErrInvalidTableName = errors.New("database: invalid table name")

// InvalidTableNameError reports that a configured table name is not a valid
// SQL identifier.
type InvalidTableNameError struct {
	Name string
}

// Error implements the error interface.
func (e *InvalidTableNameError) Error() string {
	return fmt.Sprintf("database: invalid table name %q: must match [A-Za-z_][A-Za-z0-9_]*", e.Name)
}

// Unwrap allows callers to match ErrInvalidTableName with errors.Is.
func (e *InvalidTableNameError) Unwrap() error {
	return ErrInvalidTableName
}
