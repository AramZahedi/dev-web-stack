// Read-only audit reader for a copied devstack state.db.
//
// It opens the database read-only and prints schema plus audit metadata only.
// Detail JSON is never printed; only its length and whether it mentions a
// password-like key are reported.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sqlite-audit <state.db-copy>")
		os.Exit(2)
	}
	path, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer db.Close()

	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Printf("integrity_check=%s\n", integrity)

	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		tables = append(tables, name)
	}
	_ = rows.Close()
	fmt.Printf("tables=%d\n", len(tables))
	for _, name := range tables {
		fmt.Printf("table=%s\n", name)
	}

	var schema string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='audit_events'").Scan(&schema); err != nil {
		fmt.Fprintln(os.Stderr, "audit_events schema:", err)
		os.Exit(2)
	}
	fmt.Printf("audit_schema=%s\n", schema)

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&count); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Printf("audit_count=%d\n", count)

	audit, err := db.Query(`SELECT id, actor, service_id, site_id, object_type, object_name,
		action, result, length(detail_json),
		CASE WHEN instr(lower(detail_json), 'password') > 0 THEN 1 ELSE 0 END,
		created_at
		FROM audit_events ORDER BY created_at DESC, id DESC LIMIT 20`)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer audit.Close()
	for audit.Next() {
		var id int64
		var actor, serviceID, siteID, objectType, objectName, action, result string
		var detailLen int
		var mentionsPassword int
		var createdAt int64
		if err := audit.Scan(&id, &actor, &serviceID, &siteID, &objectType, &objectName,
			&action, &result, &detailLen, &mentionsPassword, &createdAt); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Printf("audit id=%d actor=%q service=%q site=%q object_type=%q object_name=%q action=%q result=%q detail_len=%d detail_mentions_password=%d created_at=%d\n",
			id, actor, serviceID, siteID, objectType, objectName, action, result, detailLen, mentionsPassword, createdAt)
	}
	if err := audit.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
