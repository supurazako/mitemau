package main

import (
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})

	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}
