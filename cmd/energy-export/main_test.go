package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/internal/history"
)

func TestExportSeparatesJSONDataAndLogs(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"100"],[%d,"100"],[%d,"100"]]}]}}`, start.Unix(), start.Add(15*time.Second).Unix(), start.Add(30*time.Second).Unix())
	}))
	defer server.Close()
	w := history.Workload{Namespace: "training", Name: "job", UID: "job-uid", Start: start, End: start.Add(30 * time.Second), PowerQuery: "power", MeasurementMode: "estimated"}
	data, err := json.Marshal([]history.Workload{w})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "workloads.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if err := runWithDiagnostics(context.Background(), []string{"-workloads", input, "-prometheus", server.URL}, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var records []history.Record
	if err := json.Unmarshal(out.Bytes(), &records); err != nil || len(records) != 1 {
		t.Fatalf("data polluted by logs: %s", out.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(diagnostics.String()), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil || event["msg"] == nil {
			t.Fatalf("invalid diagnostic log: %s", line)
		}
	}
	if !strings.Contains(diagnostics.String(), "Completed energy dataset export") {
		t.Fatal("missing dataset completion")
	}
}
