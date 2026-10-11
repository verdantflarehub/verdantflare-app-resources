package resources

// These are the public Station resource facts, not Station's database models.
// Missing observations remain null through aggregation and presentation.
type GPU struct {
	Index        int      `json:"index"`
	UUID         string   `json:"uuid"`
	Device       string   `json:"device"`
	ModelName    string   `json:"model_name"`
	SampledAt    string   `json:"sampled_at"`
	Quality      string   `json:"quality"`
	FreeVRAMMB   *float64 `json:"free_vram_mb"`
	TotalVRAMMB  *float64 `json:"total_vram_mb"`
	UsedVRAMMB   *float64 `json:"used_vram_mb"`
	Utilization  *float64 `json:"utilization"`
	TemperatureC *float64 `json:"temperature_c"`
	PowerWatts   *float64 `json:"power_watts"`
}

type Disk struct {
	Mountpoint  string   `json:"mountpoint"`
	Device      string   `json:"device"`
	FSType      string   `json:"fstype"`
	TotalBytes  *float64 `json:"total_bytes"`
	AvailBytes  *float64 `json:"avail_bytes"`
	UsedBytes   *float64 `json:"used_bytes"`
	UsedPercent *float64 `json:"used_percent"`
}

type Node struct {
	SampledAt             string   `json:"sampled_at"`
	Quality               string   `json:"quality"`
	CPUCores              *float64 `json:"cpu_cores"`
	CPUUtilizationPercent *float64 `json:"cpu_utilization_percent"`
	CPULoad1              *float64 `json:"cpu_load1"`
	CPULoad5              *float64 `json:"cpu_load5"`
	CPULoad15             *float64 `json:"cpu_load15"`
	MemTotalBytes         *float64 `json:"mem_total_bytes"`
	MemAvailableBytes     *float64 `json:"mem_available_bytes"`
	MemUsedBytes          *float64 `json:"mem_used_bytes"`
	MemBuffersBytes       *float64 `json:"mem_buffers_bytes"`
	MemCachedBytes        *float64 `json:"mem_cached_bytes"`
	MemUsedPercent        *float64 `json:"mem_used_percent"`
	StorageDisks          []Disk   `json:"storage_disks"`
}

type Metric struct {
	Value     *float64 `json:"value"`
	Request   *float64 `json:"request"`
	Limit     *float64 `json:"limit"`
	SampledAt *string  `json:"sampled_at"`
	Quality   string   `json:"quality"`
	Reason    string   `json:"reason,omitempty"`
	Scope     string   `json:"scope"`
	Unit      string   `json:"unit"`
}

type Container struct {
	Name       string   `json:"name"`
	Ready      bool     `json:"ready"`
	Restarts   int      `json:"restarts"`
	CPU        Metric   `json:"cpu"`
	Memory     Metric   `json:"memory"`
	GPURequest *float64 `json:"gpu_request"`
	GPULimit   *float64 `json:"gpu_limit"`
}

type Workload struct {
	Name          string            `json:"name"`
	Namespace     string            `json:"namespace"`
	PodName       string            `json:"pod_name"`
	PodUID        string            `json:"pod_uid"`
	InstanceAlias string            `json:"instance_alias"`
	Status        string            `json:"status"`
	CreatedAt     string            `json:"created_at"`
	Labels        map[string]string `json:"labels,omitempty"`
	Containers    []Container       `json:"containers"`
	DisplayName   string            `json:"display_name"`
	Age           string            `json:"age"`
	Type          string            `json:"type"`
}

type Snapshot struct {
	SchemaVersion int        `json:"schema_version"`
	ObservedAt    string     `json:"observed_at"`
	HardwareState string     `json:"hardware_state"`
	GPUs          []GPU      `json:"gpus"`
	Node          *Node      `json:"node"`
	Workloads     []Workload `json:"workloads"`
}

type Summary struct {
	TotalGPUs          *float64 `json:"total_gpus"`
	TotalVRAMMB        *float64 `json:"total_vram_mb"`
	UsedVRAMMB         *float64 `json:"used_vram_mb"`
	FreeVRAMMB         *float64 `json:"free_vram_mb"`
	AverageGPUUtil     *float64 `json:"average_gpu_util"`
	HostCPUCores       *float64 `json:"host_cpu_cores"`
	HostCPUUtilization *float64 `json:"host_cpu_utilization"`
	HostCPULoad1       *float64 `json:"host_cpu_load1"`
	HostMemTotalGB     *float64 `json:"host_mem_total_gb"`
	HostMemUsedGB      *float64 `json:"host_mem_used_gb"`
	HostMemUsedPercent *float64 `json:"host_mem_used_percent"`
	StorageTotalTB     *float64 `json:"storage_total_tb"`
	StorageUsedTB      *float64 `json:"storage_used_tb"`
	StorageUsedPercent *float64 `json:"storage_used_percent"`
	Status             string   `json:"status"`
}

type WorkloadSummary struct {
	TotalPods            int     `json:"total_pods"`
	GPUPods              int     `json:"gpu_pods"`
	InfraPods            int     `json:"infra_pods"`
	TotalGPURequested    float64 `json:"total_gpu_requested"`
	TotalCPURequested    float64 `json:"total_cpu_requested"`
	TotalMemoryRequested float64 `json:"total_memory_requested"`
}

type Dashboard struct {
	SchemaVersion    int             `json:"schema_version"`
	ObservedAt       string          `json:"observed_at"`
	HardwareState    string          `json:"hardware_state"`
	Summary          Summary         `json:"summary"`
	GPUs             []GPU           `json:"gpus"`
	Node             *Node           `json:"node"`
	Workloads        []Workload      `json:"workloads"`
	WorkloadsSummary WorkloadSummary `json:"workloads_summary"`
}
