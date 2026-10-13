//go:build spike

package dbcapture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// PagesHash is the SHA-256 of every page of the database as SQLite sees it
// (WAL included), read through sqlite_dbpage: the bytes the file has after a
// full checkpoint. Reading through SQLite, not os.Open, matters on Linux:
// closing any descriptor of the database file drops the POSIX locks SQLite
// holds on it in this process.
func PagesHash(ctx context.Context, q querier) (string, int, error) { return pagesHash(ctx, q, false) }

// MaskedPagesHash is PagesHash with the two header fields the pager of each
// side rewrites on every commit that touches page 1 set to zero: the file
// change counter (bytes 24-27) and version-valid-for (bytes 92-95).
func MaskedPagesHash(ctx context.Context, q querier) (string, int, error) {
	return pagesHash(ctx, q, true)
}

func pagesHash(ctx context.Context, q querier, mask bool) (string, int, error) {
	rows, err := q.QueryContext(ctx, `SELECT data FROM sqlite_dbpage ORDER BY pgno`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	h := sha256.New()
	n := 0
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return "", 0, err
		}
		if mask && n == 0 {
			copy(b[24:28], make([]byte, 4))
			copy(b[92:96], make([]byte, 4))
		}
		h.Write(b)
		n++
	}
	return hex.EncodeToString(h.Sum(nil)), n, rows.Err()
}

// DiffPages lists the page numbers whose bytes differ, and the byte offsets
// of the first differences of page 1 (the header).
func DiffPages(ctx context.Context, a, b querier) ([]int, []int, error) {
	pa, err := pages(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	pb, err := pages(ctx, b)
	if err != nil {
		return nil, nil, err
	}
	var diff []int
	for i := 0; i < max(len(pa), len(pb)); i++ {
		if i >= len(pa) || i >= len(pb) || string(pa[i]) != string(pb[i]) {
			diff = append(diff, i+1)
		}
	}
	var hdr []int
	if len(pa) > 0 && len(pb) > 0 {
		for i := range min(len(pa[0]), len(pb[0])) {
			if pa[0][i] != pb[0][i] {
				hdr = append(hdr, i)
			}
		}
	}
	return diff, hdr, nil
}

func pages(ctx context.Context, q querier) ([][]byte, error) {
	rows, err := q.QueryContext(ctx, `SELECT data FROM sqlite_dbpage ORDER BY pgno`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LogicalHash is the SHA-256 of a logical dump: the schema and every row of
// every table, in rowid order (or primary key order for WITHOUT ROWID).
// skip lists tables left out (the vault tables of the excluded machine).
func LogicalHash(ctx context.Context, q querier, skip ...string) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT type, name, tbl_name, coalesce(sql, '') FROM sqlite_schema ORDER BY type, name`)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	var tables []string
	for rows.Next() {
		var typ, name, tbl, s string
		if err := rows.Scan(&typ, &name, &tbl, &s); err != nil {
			rows.Close()
			return "", err
		}
		if contains(skip, tbl) {
			continue
		}
		fmt.Fprintf(h, "%s|%s|%s|%s\n", typ, name, tbl, s)
		if typ == "table" && !strings.HasPrefix(name, "sqlite_") {
			tables = append(tables, name)
		}
	}
	rows.Close()
	sort.Strings(tables)
	for _, t := range tables {
		r, err := q.QueryContext(ctx, `SELECT * FROM "`+t+`" ORDER BY 1`)
		if err != nil {
			return "", err
		}
		cols, _ := r.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for r.Next() {
			if err := r.Scan(ptrs...); err != nil {
				r.Close()
				return "", err
			}
			fmt.Fprintf(h, "%s:", t)
			for _, v := range vals {
				fmt.Fprintf(h, "%T=%v|", v, v)
			}
			h.Write([]byte{'\n'})
		}
		r.Close()
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// IntegrityCheck runs PRAGMA integrity_check and returns "ok" or the problems.
func IntegrityCheck(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		out = append(out, s)
	}
	return strings.Join(out, "; "), rows.Err()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
