package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestServeExplicitLibraryFlags(t *testing.T) {
	t.Setenv("ENGINE_LIBRARY_PATHS", "/missing/ignored")
	testServeLibraries(t, false)
}

func TestServeEnvironmentLibraries(t *testing.T) {
	testServeLibraries(t, true)
}

func testServeLibraries(t *testing.T, useEnvironment bool) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	first, second := createTestLibrary(t), createTestLibrary(t)
	previous := libDirFlag
	libDirFlag = first
	defer func() { libDirFlag = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := newAPICommand()
	addresses := make(apiAddressWriter, 1)
	command.SetOut(addresses)
	if useEnvironment {
		t.Setenv("ENGINE_LIBRARY_PATHS", " ; "+first+";"+second+";;"+second+"; ")
		command.SetArgs([]string{"serve", "--port", "0"})
	} else {
		command.SetArgs([]string{"serve", "--port", "0", "--library", first, "--library", second})
	}
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	var address string
	select {
	case address = <-addresses:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timed out")
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(address + "/v1/libraries")
	if err != nil {
		t.Fatal(err)
	}
	var libraries []json.RawMessage
	decodeErr := json.NewDecoder(response.Body).Decode(&libraries)
	closeErr := response.Body.Close()
	if decodeErr != nil || closeErr != nil || len(libraries) != 2 {
		t.Fatalf("opened: %v decode=%v close=%v", libraries, decodeErr, closeErr)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

func TestServeInvalidEnvironmentLibrary(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ENGINE_LIBRARY_PATHS", filepath.Join(t.TempDir(), "missing"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command := newAPICommand()
	command.SetArgs([]string{"serve", "--port", "0"})
	if err := command.ExecuteContext(ctx); err == nil {
		t.Fatal("invalid environment library must prevent startup")
	}
}
