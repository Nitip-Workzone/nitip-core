package testutil

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// NewMockDB creates a mock database connection using sqlmock and returns a bun.DB client.
func NewMockDB(t *testing.T) (*bun.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	bunDB := bun.NewDB(db, pgdialect.New())

	t.Cleanup(func() {
		_ = bunDB.Close()
		_ = db.Close()
	})

	return bunDB, mock
}

// ExpectCommit mock standard transaction commit
func ExpectCommit(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectCommit()
}

// ExpectRollback mock standard transaction rollback
func ExpectRollback(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectRollback()
}
