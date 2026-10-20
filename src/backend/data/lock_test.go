package data

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

// 001_init.sql is locked: it is the deployed schema. Change the database with a new db/002_<name>.sql
// (see the README) and never edit this file. Line endings are normalised so a CRLF checkout hashes the same.
const lockedInitSHA256 = "20413e450d577d96e7ec5433a4d5451aafe8e86c01378228e9f8cf24ccd7c2b9"

func TestInitMigrationLocked(t *testing.T) {
	b, err := os.ReadFile("../../../db/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
	if got := hex.EncodeToString(sum[:]); got != lockedInitSHA256 {
		t.Fatalf("db/001_init.sql was changed (sha256 %s). It is locked: add db/002_<name>.sql instead", got)
	}
}
