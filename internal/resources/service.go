package resources

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

type Identity struct{ UserID, OrganizationID, StationID string }
type Source interface {
	Read(context.Context, Identity) (Snapshot, error)
}
type WorkspaceSource interface {
	Workspaces(context.Context, Identity) (WorkspacesResponse, error)
	ReleaseWorkspace(context.Context, Identity, ReleaseWorkspaceInput) (ReleaseWorkspaceResponse, error)
}
type Service struct {
	Source Source
	Now    func() time.Time
}

func (s Service) Workspaces(ctx context.Context, identity Identity) (WorkspacesResponse, error) {
	ws, ok := s.Source.(WorkspaceSource)
	if !ok {
		return WorkspacesResponse{}, &SourceError{Status: 503, Code: "STATION_UNAVAILABLE"}
	}
	return ws.Workspaces(ctx, identity)
}

func (s Service) ReleaseWorkspace(ctx context.Context, identity Identity, input ReleaseWorkspaceInput) (ReleaseWorkspaceResponse, error) {
	ws, ok := s.Source.(WorkspaceSource)
	if !ok {
		return ReleaseWorkspaceResponse{}, &SourceError{Status: 503, Code: "STATION_UNAVAILABLE"}
	}
	return ws.ReleaseWorkspace(ctx, identity, input)
}

func valid(v *float64) bool     { return v != nil && *v >= 0 && !math.IsNaN(*v) && !math.IsInf(*v, 0) }
func number(v float64) *float64 { return &v }
func scaled(v *float64, divisor float64) *float64 {
	if !valid(v) {
		return nil
	}
	return number(*v / divisor)
}
func total(values []*float64) *float64 {
	sum := 0.0
	for _, v := range values {
		if !valid(v) {
			return nil
		}
		sum += *v
		if math.IsInf(sum, 0) {
			return nil
		}
	}
	return number(sum)
}
func percent(used, capacity *float64) *float64 {
	if !valid(used) || !valid(capacity) || *capacity <= 0 {
		return nil
	}
	return number(*used / *capacity * 100)
}

func (s Service) Dashboard(ctx context.Context, identity Identity) (Dashboard, error) {
	raw, err := s.Source.Read(ctx, identity)
	if err != nil {
		return Dashboard{}, err
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return Project(raw, now)
}

// Project owns the Resources product's totals, classifications and alert thresholds.
// Station retains collection, sample attribution and scheduling.
func Project(raw Snapshot, now time.Time) (Dashboard, error) {
	observed, err := time.Parse(time.RFC3339Nano, raw.ObservedAt)
	if raw.SchemaVersion != 1 || err != nil || observed.After(now.Add(5*time.Second)) || raw.Workloads == nil {
		return Dashboard{}, errors.New("invalid resource snapshot")
	}
	state := raw.HardwareState
	if state != "fresh" && state != "stale" && state != "unavailable" {
		return Dashboard{}, errors.New("invalid hardware quality")
	}
	if now.Sub(observed) > 30*time.Second {
		state = "stale"
	}
	out := Dashboard{SchemaVersion: 1, ObservedAt: raw.ObservedAt, HardwareState: state, GPUs: []GPU{}, Workloads: []Workload{}, Summary: Summary{Status: "Unavailable"}}
	if state == "fresh" {
		for _, g := range raw.GPUs {
			if freshHardware(g.Quality, g.SampledAt, now) {
				out.GPUs = append(out.GPUs, g)
			}
		}
		if raw.GPUs == nil || len(out.GPUs) != len(raw.GPUs) {
			out.GPUs = nil
		}
		if raw.Node != nil && freshHardware(raw.Node.Quality, raw.Node.SampledAt, now) {
			out.Node = raw.Node
		}
		out.Summary = summarize(out.GPUs, out.Node)
		if out.GPUs == nil && out.Node == nil {
			out.HardwareState, out.Summary.Status = "unavailable", "Unavailable"
		}
	}
	seen := map[string]bool{}
	for _, source := range raw.Workloads {
		if source.PodUID == "" || source.Namespace == "" || source.PodName == "" || seen[source.PodUID] {
			return Dashboard{}, errors.New("ambiguous workload identity")
		}
		seen[source.PodUID] = true
		// A succeeded Pod is a finished one-shot record. Current tasks are still running, waiting, or failed.
		if source.Status == "Succeeded" {
			continue
		}
		w := source
		w.Containers = append([]Container{}, source.Containers...)
		w.Type = "infra"
		w.DisplayName = w.Labels["app.kubernetes.io/name"]
		if w.DisplayName == "" {
			w.DisplayName = w.Name
		}
		w.Labels = nil
		w.Age = age(w.CreatedAt, now)
		names := map[string]bool{}
		for i := range w.Containers {
			c := &w.Containers[i]
			if c.Name == "" || names[c.Name] {
				return Dashboard{}, errors.New("ambiguous container identity")
			}
			names[c.Name] = true
			c.CPU = safeMetric(c.CPU, "cores", now)
			c.Memory = safeMetric(c.Memory, "bytes", now)
			if valid(c.GPURequest) && *c.GPURequest > 0 || valid(c.GPULimit) && *c.GPULimit > 0 {
				w.Type = "gpu"
			}
			if w.Status == "Running" || w.Status == "Pending" {
				if valid(c.CPU.Request) {
					out.WorkloadsSummary.TotalCPURequested += *c.CPU.Request
				}
				if valid(c.Memory.Request) {
					out.WorkloadsSummary.TotalMemoryRequested += *c.Memory.Request
				}
				if valid(c.GPURequest) {
					out.WorkloadsSummary.TotalGPURequested += *c.GPURequest
				}
			}
		}
		out.WorkloadsSummary.TotalPods++
		if w.Type == "gpu" {
			out.WorkloadsSummary.GPUPods++
		} else {
			out.WorkloadsSummary.InfraPods++
		}
		out.Workloads = append(out.Workloads, w)
	}
	return out, nil
}

func freshHardware(quality, sampled string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, sampled)
	return quality == "fresh" && err == nil && !t.After(now.Add(5*time.Second)) && now.Sub(t) <= 30*time.Second
}

func summarize(gpus []GPU, node *Node) Summary {
	s := Summary{Status: "Healthy"}
	if gpus != nil {
		s.TotalGPUs = number(float64(len(gpus)))
		capacity, used, free, util := []*float64{}, []*float64{}, []*float64{}, []*float64{}
		for _, g := range gpus {
			capacity = append(capacity, g.TotalVRAMMB)
			used = append(used, g.UsedVRAMMB)
			free = append(free, g.FreeVRAMMB)
			util = append(util, g.Utilization)
			if valid(g.TemperatureC) && *g.TemperatureC > 80 {
				s.Status = "Warning"
			}
		}
		s.TotalVRAMMB, s.UsedVRAMMB, s.FreeVRAMMB = total(capacity), total(used), total(free)
		if len(gpus) > 0 {
			s.AverageGPUUtil = scaled(total(util), float64(len(gpus)))
		}
	}
	if node != nil {
		s.HostCPUCores, s.HostCPUUtilization, s.HostCPULoad1 = node.CPUCores, node.CPUUtilizationPercent, node.CPULoad1
		s.HostMemTotalGB, s.HostMemUsedGB, s.HostMemUsedPercent = scaled(node.MemTotalBytes, 1<<30), scaled(node.MemUsedBytes, 1<<30), node.MemUsedPercent
		if node.StorageDisks != nil {
			capacity, used := []*float64{}, []*float64{}
			for _, disk := range node.StorageDisks {
				capacity = append(capacity, disk.TotalBytes)
				used = append(used, disk.UsedBytes)
			}
			t, u := total(capacity), total(used)
			s.StorageTotalTB, s.StorageUsedTB, s.StorageUsedPercent = scaled(t, 1e12), scaled(u, 1e12), percent(u, t)
		}
		if valid(node.MemUsedPercent) && *node.MemUsedPercent > 90 {
			s.Status = "Degraded"
		}
	}
	if gpus == nil || node == nil {
		s.Status = "Partial"
	}
	return s
}

func safeMetric(m Metric, unit string, now time.Time) Metric {
	if !valid(m.Request) {
		m.Request = nil
	}
	if !valid(m.Limit) {
		m.Limit = nil
	}
	var sampled time.Time
	if m.SampledAt != nil {
		sampled, _ = time.Parse(time.RFC3339Nano, *m.SampledAt)
	}
	if m.Quality == "fresh" && m.Unit == unit && m.Scope == "instance_container" && valid(m.Value) && !sampled.IsZero() && !sampled.After(now.Add(5*time.Second)) && now.Sub(sampled) <= 30*time.Second {
		return m
	}
	m.Value = nil
	if m.Quality == "stale" || !sampled.IsZero() && now.Sub(sampled) > 30*time.Second {
		m.Quality, m.Reason = "stale", "SAMPLE_STALE"
	} else {
		m.Quality, m.Reason = "unavailable", "SAMPLE_UNAVAILABLE"
	}
	return m
}

func age(created string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil || t.After(now) {
		return "—"
	}
	d := now.Sub(t)
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
