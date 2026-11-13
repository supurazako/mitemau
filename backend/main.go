package main

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
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
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 10 * time.Second
	cfg.WriteTimeout = 10 * time.Second

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

	verificationCfg, sender, err := emailConfig()
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	verification := newVerificationServer(db, sender, verificationCfg)
	verification.routes(mux)
	go verification.runCleanup(context.Background())
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})

	server := &http.Server{Addr: envDefault("API_ADDR", "localhost:8080"), Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
