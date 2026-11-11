// Package env reads service configuration from environment variables.
package env

import (
	"fmt"
	"os"
	"strconv"
)

// Reader accumulates the first configuration error so that call sites stay linear.
type Reader struct {
	lookup func(string) (string, bool)
	err    error
}

// New returns a reader over the process environment.
func New() *Reader { return &Reader{lookup: os.LookupEnv} }

// Err returns the first error encountered.
func (r *Reader) Err() error { return r.err }

// String returns the variable or def if it is unset or empty.
func (r *Reader) String(key, def string) string {
	if v, ok := r.lookup(key); ok && v != "" {
		return v
	}
	return def
}

// Required returns the variable and records an error if it is unset or empty.
func (r *Reader) Required(key string) string {
	v := r.String(key, "")
	if v == "" && r.err == nil {
		r.err = fmt.Errorf("environment variable %s is required", key)
	}
	return v
}

// Bool parses a boolean variable.
func (r *Reader) Bool(key string, def bool) bool {
	v := r.String(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil && r.err == nil {
		r.err = fmt.Errorf("environment variable %s: %w", key, err)
	}
	return b
}
