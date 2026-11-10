package history

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func workload() Workload {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return Workload{Namespace: "training", Name: "job-a", UID: "uid-1", CPUCores: 2, MemoryBytes: 1073741824, GPUs: 1, Priority: 10, Start: start, End: start.Add(time.Hour), PowerQuery: `sum(workload_cpu_watts{pod="job-a-pod"})`, MeasurementMode: "estimated"}
}

func historyServer(t *testing.T, mutate func([][2]any) [][2]any) *httptest.Server {
	t.Helper()
	w := workload()
	srv := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" || r.URL.Query().Get("query") != w.PowerQuery || r.URL.Query().Get("step") != "900.000000000" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		samples := [][2]any{{w.Start.Unix(), "100"}, {w.Start.Add(15 * time.Minute).Unix(), "200"}, {w.Start.Add(30 * time.Minute).Unix(), "300"}, {w.Start.Add(45 * time.Minute).Unix(), "200"}, {w.End.Unix(), "100"}}
		if mutate != nil {
			samples = mutate(samples)
		}
		if err := json.NewEncoder(out).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"values": samples}}}}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExportIntegratesEnergyAndPreservesMetadata(t *testing.T) {
	server := historyServer(t, nil)
	w := workload()
	got, err := (Exporter{PrometheusURL: server.URL, Step: 15 * time.Minute}).Export(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.EnergyWh-200) > 1e-9 || got.RuntimeSeconds != 3600 || got.SampleCount != 5 || got.Workload != w {
		t.Fatalf("wrong record: %+v", got)
	}
}

func TestIncompleteAndInvalidHistory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([][2]any) [][2]any
	}{
		{"missing", func(s [][2]any) [][2]any { return s[:4] }},
		{"negative", func(s [][2]any) [][2]any { s[2][1] = "-1"; return s }},
		{"NaN", func(s [][2]any) [][2]any { s[2][1] = "NaN"; return s }},
		{"out of order", func(s [][2]any) [][2]any { s[2][0] = s[1][0]; return s }},
		{"gap", func(s [][2]any) [][2]any { s[2][0] = workload().Start.Add(35 * time.Minute).Unix(); return s }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := historyServer(t, tc.mutate)
			if _, err := (Exporter{PrometheusURL: server.URL, Step: 15 * time.Minute}).Export(context.Background(), workload()); err == nil {
				t.Fatal("accepted incomplete history")
			}
		})
	}
}

func TestHistoryHTTPFailures(t *testing.T) {
	for _, body := range []string{"bad JSON", `{"status":"error"}`, `{"status":"success","data":{"resultType":"matrix","result":[]}}`, `{"status":"success","warnings":["partial"],"data":{"resultType":"matrix","result":[{}]}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			if _, err := (Exporter{PrometheusURL: server.URL}).Export(context.Background(), workload()); err == nil {
				t.Fatal("accepted failed response")
			}
		})
	}
}

func TestHistoryValidation(t *testing.T) {
	for _, change := range []func(*Workload){
		func(w *Workload) { w.UID = "" }, func(w *Workload) { w.PowerQuery = "" }, func(w *Workload) { w.End = w.Start },
		func(w *Workload) { w.CPUCores = math.NaN() }, func(w *Workload) { w.GPUs = -1 }, func(w *Workload) { w.MeasurementMode = "" },
	} {
		w := workload()
		change(&w)
		if _, err := (Exporter{PrometheusURL: "http://unused"}).Export(context.Background(), w); err == nil {
			t.Fatalf("accepted %+v", w)
		}
	}
}

func TestExplicitBoundaryEstimation(t *testing.T) {
	server := historyServer(t, func(samples [][2]any) [][2]any { return samples[:4] })
	got, err := (Exporter{PrometheusURL: server.URL, Step: 15 * time.Minute, AllowEdgeEstimates: true}).Export(context.Background(), workload())
	if err != nil {
		t.Fatal(err)
	}
	if got.EstimatedBoundarySeconds != 900 || math.Abs(got.EnergyWh-212.5) > 1e-9 {
		t.Fatalf("wrong bounded estimate: %+v", got)
	}
}
