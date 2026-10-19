package resources

import (
	"testing"
	"time"
)

func fixture(now time.Time) Snapshot {
	stamp := now.Format(time.RFC3339Nano)
	metric := Metric{Value: number(0), Request: number(2), Limit: number(8), SampledAt: &stamp, Quality: "fresh", Scope: "instance_container", Unit: "cores"}
	return Snapshot{SchemaVersion: 1, ObservedAt: stamp, HardwareState: "fresh",
		GPUs:      []GPU{{UUID: "gpu-a", SampledAt: stamp, Quality: "fresh", TotalVRAMMB: number(32768), UsedVRAMMB: number(0), FreeVRAMMB: number(32768), Utilization: number(0), TemperatureC: number(81)}},
		Node:      &Node{SampledAt: stamp, Quality: "fresh", MemTotalBytes: number(16 << 30), MemUsedBytes: number(8 << 30), MemUsedPercent: number(50), StorageDisks: []Disk{{TotalBytes: number(1e12), UsedBytes: number(2e11)}}},
		Workloads: []Workload{{PodUID: "uid-a", Name: "a", PodName: "a", Namespace: "verdantflare-fixture", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), Status: "Running", Labels: map[string]string{"app.kubernetes.io/name": "Fixture"}, Containers: []Container{{Name: "main", CPU: metric, GPURequest: number(1)}}}},
	}
}

func TestSummaryAndWorkloadOwnership(t *testing.T) {
	now := time.Now().UTC()
	raw := fixture(now)
	out, err := Project(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary.Status != "Warning" || *out.Summary.TotalVRAMMB != 32768 || *out.Summary.UsedVRAMMB != 0 || *out.Summary.HostMemTotalGB != 16 || *out.Summary.StorageUsedPercent != 20 {
		t.Fatalf("incorrect summary: %+v", out.Summary)
	}
	if out.WorkloadsSummary.TotalCPURequested != 2 || out.WorkloadsSummary.TotalGPURequested != 1 || out.Workloads[0].Type != "gpu" || out.Workloads[0].DisplayName != "Fixture" || out.Workloads[0].Containers[0].CPU.Value == nil || *out.Workloads[0].Containers[0].CPU.Value != 0 {
		t.Fatal("real zero or workload projection lost")
	}
	if raw.Workloads[0].Type != "" || raw.Workloads[0].Labels == nil {
		t.Fatal("projection mutated source")
	}
}

func TestMissingAndStaleHardwareNeverBecomesZero(t *testing.T) {
	now := time.Now().UTC()
	for _, kind := range []string{"missing_capacity", "missing_gpu", "stale", "old_sample", "missing_time"} {
		t.Run(kind, func(t *testing.T) {
			raw := fixture(now)
			switch kind {
			case "missing_capacity":
				raw.GPUs[0].TotalVRAMMB = nil
			case "missing_gpu":
				raw.GPUs = nil
			case "stale":
				raw.HardwareState = "stale"
			case "old_sample":
				raw.GPUs[0].SampledAt = now.Add(-31 * time.Second).Format(time.RFC3339Nano)
			case "missing_time":
				raw.GPUs[0].SampledAt = ""
			}
			out, err := Project(raw, now)
			if err != nil {
				t.Fatal(err)
			}
			if out.Summary.TotalVRAMMB != nil {
				t.Fatal("unknown capacity became known", out.Summary.TotalVRAMMB)
			}
			if kind != "missing_capacity" && out.Summary.TotalGPUs != nil {
				t.Fatal("unavailable GPU count became zero")
			}
		})
	}
}

func TestWorkloadScopeAndFreshness(t *testing.T) {
	now := time.Now().UTC()
	for _, kind := range []string{"stale", "future", "wrong_scope", "missing", "completed"} {
		t.Run(kind, func(t *testing.T) {
			raw := fixture(now)
			m := &raw.Workloads[0].Containers[0].CPU
			switch kind {
			case "stale":
				s := now.Add(-31 * time.Second).Format(time.RFC3339Nano)
				m.SampledAt = &s
			case "future":
				s := now.Add(time.Minute).Format(time.RFC3339Nano)
				m.SampledAt = &s
			case "wrong_scope":
				m.Scope = "node"
			case "missing":
				m.Value = nil
			case "completed":
				raw.Workloads[0].Status = "Succeeded"
			}
			out, err := Project(raw, now)
			if err != nil {
				t.Fatal(err)
			}
			if kind != "completed" && out.Workloads[0].Containers[0].CPU.Value != nil {
				t.Fatal("unattributable sample exposed")
			}
			if kind == "completed" && (len(out.Workloads) != 0 || out.WorkloadsSummary.TotalPods != 0 || out.WorkloadsSummary.TotalCPURequested != 0) {
				t.Fatal("completed pod remained in current tasks")
			}
		})
	}
	mixed := fixture(now)
	finished := mixed.Workloads[0]
	finished.PodUID, finished.Name, finished.PodName, finished.Status = "uid-done", "migrate", "migrate", "Succeeded"
	failed := mixed.Workloads[0]
	failed.PodUID, failed.Name, failed.PodName, failed.Status = "uid-failed", "migrate-failed", "migrate-failed", "Failed"
	mixed.Workloads = append(mixed.Workloads, finished, failed)
	mixedOut, err := Project(mixed, now)
	if err != nil || len(mixedOut.Workloads) != 2 || mixedOut.WorkloadsSummary.TotalPods != 2 || mixedOut.Workloads[1].Status != "Failed" {
		t.Fatal("current tasks dropped a running or failed workload", err, mixedOut.WorkloadsSummary.TotalPods)
	}
	bad := fixture(now)
	bad.Workloads = append(bad.Workloads, bad.Workloads[0])
	if _, err := Project(bad, now); err == nil {
		t.Fatal("duplicate identity accepted")
	}
}
