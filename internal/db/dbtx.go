package db

import "github.com/uptrace/bun"

// dbtx is the Bun handle the repositories depend on. It's satisfied by both
// *bun.DB (production) and bun.Tx (tests), so a repository doesn't need to
// know whether it's running against the real pool or an isolated per-test
// transaction.
type dbtx = bun.IDB
