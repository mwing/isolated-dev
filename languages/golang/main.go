package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	fmt.Println("Hello from {{PROJECT_NAME}}!")
	fmt.Println("Go development environment is ready!")

	// Example HTTP server (uncomment to use)
	/*
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from {{PROJECT_NAME}}!\n")
	})

	port := ":8080"
	fmt.Printf("Server starting on http://0.0.0.0%s\n", port)
	log.Fatal(http.ListenAndServe(port, nil))
	*/
}