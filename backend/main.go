package main

import (
	"context"
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	required := []string{
		"MYSQL_HOST",
		"MYSQL_PORT",
		"MYSQL_DATABASE",
		"MYSQL_USER",
		"MYSQL_PASSWORD",
	}

	for _, name := range required {
		if os.Getenv(name) == "" {
			log.Fatalf("%s must be set", name)
		}
	}

	cfg := mysql.NewConfig()
	cfg.User = os.Getenv("MYSQL_USER")
	cfg.Passwd = os.Getenv("MYSQL_PASSWORD")
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(
		os.Getenv("MYSQL_HOST"),
		os.Getenv("MYSQL_PORT"),
	)
	cfg.DBName = os.Getenv("MYSQL_DATABASE")

	dsn := cfg.FormatDSN()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
