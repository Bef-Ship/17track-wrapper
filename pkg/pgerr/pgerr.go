// Package pgerr classifies Postgres errors by SQLSTATE instead of message text.
package pgerr

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Code returns the SQLSTATE of a (possibly wrapped) Postgres error, or "".
func Code(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// IsUniqueViolation reports a unique_violation (23505).
func IsUniqueViolation(err error) bool { return Code(err) == "23505" }

// IsForeignKeyViolation reports a foreign_key_violation (23503).
func IsForeignKeyViolation(err error) bool { return Code(err) == "23503" }

// IsPrimaryKeyViolation is true only for primary-key conflicts (safe to regenerate ids).
func IsPrimaryKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.HasSuffix(pgErr.ConstraintName, "_pkey")
}
