package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestReviewExecutesToolCallAndReturnsReport(t *testing.T) {
	snapshot := makeSnapshot(t)
	var calls atomic.Int32
	var logs bytes.Buffer
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected API path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing authorization header")
		}
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		responseBody := ""
		if calls.Add(1) == 1 {
			if len(body.Tools) != 4 {
				t.Errorf("got %d tools", len(body.Tools))
			}
			responseBody = `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"stat","arguments":"{}"}}]}}]}`
		} else {
			last := body.Messages[len(body.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "call-1" {
				t.Errorf("tool result was not appended correctly: %+v", last)
			}
			responseBody = `{"choices":[{"message":{"role":"assistant","content":"No findings."}}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody))}, nil
	})

	client, err := NewClient(Config{Endpoint: "http://model.test/v1", APIKey: "test-key", Model: "test-model", MaxSteps: 3, Verbose: true, LogWriter: &logs}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: transport}
	report, err := client.Review(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report != "No findings." || calls.Load() != 2 {
		t.Fatalf("report=%q calls=%d", report, calls.Load())
	}
	logOutput := logs.String()
	for _, expected := range []string{"model response step=1", `"name":"stat"`, "model response step=2", "No findings."} {
		if !strings.Contains(logOutput, expected) {
			t.Errorf("verbose log does not contain %q:\n%s", expected, logOutput)
		}
	}
}

func TestReviewDoesNotLogByDefault(t *testing.T) {
	client, err := NewClient(Config{Endpoint: "http://model.test/v1", Model: "test-model", MaxSteps: 1}, makeSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if client.logger != nil {
		t.Fatal("logger is enabled without Verbose")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func makeSnapshot(t *testing.T) *Snapshot {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, _ := repo.Worktree()
	if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte("package example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("."); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main", "feature"} {
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), hash)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := Open(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestNewClientRejectsNonURL(t *testing.T) {
	_, err := NewClient(Config{Endpoint: "localhost:1234", Model: "test"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid endpoint") {
		t.Fatalf("expected invalid endpoint error, got %v", err)
	}
}
