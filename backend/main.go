package main

import (
	"context"
	"database/sql"
	_ "github.com/go-sql-driver/mysql"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("MYSQL_DSN must be set")
	}

	db, err := sql.Open("mysql", open)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Backgrouond(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("connected to MySQL")

	http.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})

	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}
