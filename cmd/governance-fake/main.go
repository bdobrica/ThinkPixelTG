// Command governance-fake runs a contract-shaped local ThinkPixelAG or ThinkPixelGR fake.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/adapters/fakes"
)

func main() {
	kind := flag.String("kind", "", "fake kind: ag or gr")
	address := flag.String("address", ":8080", "listen address")
	healthcheck := flag.String("healthcheck", "", "check URL and exit")
	flag.Parse()
	if *healthcheck != "" {
		client := http.Client{Timeout: 2 * time.Second}
		response, err := client.Get(*healthcheck)
		if err != nil || response.StatusCode != 200 {
			os.Exit(1)
		}
		_ = response.Body.Close()
		return
	}
	handler, err := (fakes.Server{Kind: *kind}).Handler()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	server := &http.Server{Addr: *address, Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
