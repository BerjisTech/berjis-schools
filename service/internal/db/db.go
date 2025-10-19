package db

import (
    "github.com/jmoiron/sqlx"
    _ "github.com/jackc/pgx/v5/stdlib"
)

func Connect(dsn string) (*sqlx.DB, error) {
    return sqlx.Connect("pgx", dsn)
}