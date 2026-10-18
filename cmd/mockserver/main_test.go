package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerWritesToFileAndStdoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	logger, closeFn, err := newLogger("debug", "json", path)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("file-log-test", "request_id", "test-1")
	closeFn()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"msg":"file-log-test"`) || !strings.Contains(string(data), `"request_id":"test-1"`) {
		t.Fatalf("log file does not contain structured record: %s", data)
	}
}
