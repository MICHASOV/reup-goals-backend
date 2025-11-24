package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	// простой health-check маршрут
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	// запуск сервера
	fmt.Println("🚀 API server is running on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}