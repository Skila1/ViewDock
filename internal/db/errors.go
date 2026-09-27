package db

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsUniqueViolation reports whether err is a unique constraint violation from
// either supported backend.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "23505"
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
