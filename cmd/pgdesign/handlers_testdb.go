package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/smm-h/pgdesign/internal/testdb"
)

func handleTestdbSetup(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for testdb setup")
		return 1
	}

	ddlPath, _ := kwargs["ddl"].(string)
	if ddlPath == "" {
		fmt.Fprintln(os.Stderr, "error: --ddl is required for testdb setup")
		return 1
	}

	ddlFile, err := os.Open(ddlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open DDL file: %v\n", err)
		return 1
	}
	defer ddlFile.Close()

	manager, err := testdb.NewManager(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
		return 1
	}

	ctx := context.Background()
	db, err := manager.Create(ctx, testdb.CreateOptions{DDL: ddlFile})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create ephemeral database: %v\n", err)
		return 1
	}

	fmt.Println(db.URL)
	return 0
}

func handleTestdbTeardown(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for testdb teardown")
		return 1
	}

	u, err := url.Parse(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: parse URL: %v\n", err)
		return 1
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		fmt.Fprintln(os.Stderr, "error: URL has no database name")
		return 1
	}

	manager, err := testdb.NewManager(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
		return 1
	}

	ctx := context.Background()
	if err := manager.DropByName(ctx, dbName); err != nil {
		fmt.Fprintf(os.Stderr, "error: drop database %s: %v\n", dbName, err)
		return 1
	}

	return 0
}

func handleTestdbGC(kwargs map[string]interface{}) int {
	dbURL, _ := kwargs["db"].(string)
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db is required for testdb gc")
		return 1
	}

	olderThanStr, _ := kwargs["older-than"].(string)
	if olderThanStr == "" {
		fmt.Fprintln(os.Stderr, "error: --older-than is required for testdb gc")
		return 1
	}

	olderThan, err := time.ParseDuration(olderThanStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: parse duration %q: %v\n", olderThanStr, err)
		return 1
	}

	manager, err := testdb.NewManager(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create manager: %v\n", err)
		return 1
	}

	ctx := context.Background()
	orphans, err := manager.ListOrphans(ctx, olderThan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: list orphans: %v\n", err)
		return 1
	}

	if len(orphans) == 0 {
		fmt.Fprintln(os.Stderr, "no orphaned databases found")
		return 0
	}

	var failures int
	for _, orphan := range orphans {
		if err := manager.DropByName(ctx, orphan.Name); err != nil {
			fmt.Fprintf(os.Stderr, "error: drop %s: %v\n", orphan.Name, err)
			failures++
		} else {
			fmt.Fprintf(os.Stderr, "dropped %s\n", orphan.Name)
		}
	}

	fmt.Fprintf(os.Stderr, "dropped %d databases, %d failures\n", len(orphans)-failures, failures)
	if failures > 0 {
		return 1
	}
	return 0
}
