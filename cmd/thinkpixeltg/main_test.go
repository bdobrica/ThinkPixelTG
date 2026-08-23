package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHealthcheckEndpointContract(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(200) }), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	client := http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listener.Addr().String())
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("health endpoint: %v", err)
	}
	_ = response.Body.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}
