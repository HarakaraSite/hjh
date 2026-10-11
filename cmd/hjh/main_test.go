package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServeOccupiedPortDoesNotOpenHistory(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	workspace := t.TempDir()
	if code := serve(context.Background(), workspace, "", "", "", "", "", 0, 0, listener.Addr().String(), io.Discard); code != 1 {
		t.Fatalf("losing startup exit code = %d", code)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".hjh", "history.sqlite3")); !os.IsNotExist(err) {
		t.Fatalf("losing startup opened history: %v", err)
	}
}

func TestServeStopsAfterCoreShutdownRequest(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workspace := t.TempDir()
	finished := make(chan int, 1)
	go func() {
		finished <- serve(ctx, workspace, "", "", "", "", "", 0, 0, address, io.Discard)
	}()
	client := &http.Client{Timeout: 300 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	coreID := ""
	for time.Now().Before(deadline) {
		response, requestErr := client.Get("http://" + address + "/api/v2/core")
		if requestErr == nil {
			var core struct {
				CoreID string `json:"coreId"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&core)
			_ = response.Body.Close()
			if decodeErr == nil && response.StatusCode == http.StatusOK {
				coreID = core.CoreID
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if coreID == "" {
		t.Fatal("serve did not expose its Core")
	}
	body, err := json.Marshal(struct {
		CoreID string `json:"coreId"`
	}{CoreID: coreID})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post("http://"+address+"/api/v2/core/shutdown", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Core shutdown request: %v", err)
	}
	var acknowledgment struct {
		OK bool `json:"ok"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&acknowledgment)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted || decodeErr != nil || !acknowledgment.OK {
		t.Fatalf("Core shutdown response = %d ok=%t err=%v", response.StatusCode, acknowledgment.OK, decodeErr)
	}
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatalf("serve exit code = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not close after Core shutdown")
	}
}
