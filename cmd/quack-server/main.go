package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"quack/internal/server"
	"quack/internal/sqlitedb"
)

func main() {
	root := flag.String("root", "", "root directory for blob storage")
	databasePath := flag.String("database", "", "sqlite database file")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "-root is required")
		os.Exit(1)
	}
	if *databasePath == "" {
		fmt.Fprintln(os.Stderr, "-database is required")
		os.Exit(1)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store, err := server.NewBlobStorage(*root)
	if err != nil {
		log.Fatal(err)
	}
	db, err := sqlitedb.Open(context.Background(), *databasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	srv := server.New(addr, os.Getenv("UPLOAD_TOKEN"), store, db)
	log.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
