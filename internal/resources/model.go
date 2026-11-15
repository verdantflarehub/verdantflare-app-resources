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

type WorkspaceVolume struct {
	WorkspaceID    string   `json:"workspace_id"`
	PVCName        string   `json:"pvc_name"`
	PVName         string   `json:"pv_name"`
	Namespace      string   `json:"namespace"`
	PoolName       string   `json:"pool_name"`
	AppID          string   `json:"app_id"`
	AppDisplayName string   `json:"app_display_name"`
	StorageBytes   int64    `json:"storage_bytes"`
	StorageClass   string   `json:"storage_class"`
	AccessMode     string   `json:"access_mode"`
	NodeName       string   `json:"node_name"`
	InstanceID     string   `json:"instance_id,omitempty"`
	InstanceName   string   `json:"instance_name,omitempty"`
	OrganizationID string   `json:"organization_id,omitempty"`
	ProjectID      string   `json:"project_id,omitempty"`
	InstallationID string   `json:"installation_id,omitempty"`
	State          string   `json:"state"`
	AllowedActions []string `json:"allowed_actions"`
	BoundAt        string   `json:"bound_at,omitempty"`
	RetainedAt     string   `json:"retained_at,omitempty"`
}

type WorkspacesResponse struct {
	Workspaces []WorkspaceVolume `json:"workspaces"`
	RequestID  string            `json:"request_id,omitempty"`
}

type ReleaseWorkspaceInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	Namespace      string `json:"namespace"`
	PVCName        string `json:"pvc_name"`
}

type ReleaseWorkspaceResponse struct {
	Status    string          `json:"status"`
	Workspace WorkspaceVolume `json:"workspace"`
	RequestID string          `json:"request_id,omitempty"`
}

type GPUSchedulerCard struct {
	NodeUID                    string  `json:"node_uid"`
	NodeName                   string  `json:"node_name"`
	GPUUUID                    string  `json:"gpu_uuid"`
	GPUIndex                   int     `json:"gpu_index"`
	ModelName                  string  `json:"model_name"`
	TotalVRAMMiB               int64   `json:"total_vram_mib"`
	DriverOverheadMiB          int64   `json:"driver_overhead_mib"`
	HeadroomMiB                int64   `json:"headroom_mib"`
	PinnedMiB                  int64   `json:"pinned_mib"`
	LeasedMiB                  int64   `json:"leased_mib"`
	EvictableMiB               int64   `json:"evictable_mib"`
	QuarantinedMiB             int64   `json:"quarantined_mib"`
	UnattributedExternalMiB    int64   `json:"unattributed_external_mib"`
	UnmaterializedBudgetMiB    int64   `json:"unmaterialized_budget_mib"`
	LedgerUncommittedMiB       int64   `json:"ledger_uncommitted_mib"`
	MeasuredFreeVRAMMiB        int64   `json:"measured_free_vram_mib"`
	MeasuredUsedVRAMMiB        int64   `json:"measured_used_vram_mib"`
	GrantableStandardMiB       int64   `json:"grantable_standard_mib"`
	TelemetryFresh             bool    `json:"telemetry_fresh"`
	AdmissionOpen              bool    `json:"admission_open"`
	TelemetryBootID            string  `json:"telemetry_boot_id,omitempty"`
	TelemetrySampleID          int64   `json:"telemetry_sample_id"`
	TelemetrySampledAt         string  `json:"telemetry_sampled_at,omitempty"`
	FencingEpoch               int64   `json:"fencing_epoch"`
	MinFencingToken            int64   `json:"min_fencing_token"`
	ReservedByLeaseID          *string `json:"reserved_by_lease_id,omitempty"`
	TimeSlicingSlots           int     `json:"time_slicing_slots"`
	AllocatedK8sSlots          int     `json:"allocated_k8s_slots"`
	ScalingUpK8sSlots          int     `json:"scaling_up_k8s_slots"`
	UnscheduledPendingK8sSlots int     `json:"unscheduled_pending_k8s_slots"`
	AdmissionMode              string  `json:"admission_mode"`
	State                      string  `json:"state"`
	StateReason                string  `json:"state_reason"`
	LastEventSeq               int64   `json:"last_event_seq"`
	LastEventHash              string  `json:"last_event_hash"`
}

type GPUSchedulerLease struct {
	LeaseID           string   `json:"lease_id"`
	State             string   `json:"state"`
	HolderAppID       string   `json:"holder_app_id"`
	HolderTaskID      string   `json:"holder_task_id"`
	AttemptID         string   `json:"attempt_id"`
	StageID           string   `json:"stage_id"`
	NodeUID           string   `json:"node_uid"`
	PodUID            string   `json:"pod_uid,omitempty"`
	ContainerID       string   `json:"container_id,omitempty"`
	BootID            string   `json:"boot_id,omitempty"`
	GPUUUID           string   `json:"gpu_uuid,omitempty"`
	GPUUUIDs          []string `json:"gpu_uuids"`
	ReservedGPUUUIDs  []string `json:"reserved_gpu_uuids,omitempty"`
	PeakBudgetMiB     int64    `json:"peak_budget_mib"`
	StartupFreeMinMiB int64    `json:"startup_free_min_mib"`
	FencingToken      int64    `json:"fencing_token"`
	Priority          string   `json:"priority"`
	QueuePosition     int      `json:"queue_position,omitempty"`
	BlockingReason    string   `json:"blocking_reason"`
	BlockingHolderID  string   `json:"blocking_holder_id,omitempty"`
	ExpiresAt         string   `json:"expires_at"`
	Version           int64    `json:"version"`
	LedgerEventSeq    int64    `json:"ledger_event_seq,omitempty"`
}

type GPUSchedulerResidency struct {
	InstanceID           string `json:"instance_id"`
	AppID                string `json:"app_id"`
	NodeUID              string `json:"node_uid"`
	PodUID               string `json:"pod_uid"`
	BootID               string `json:"boot_id"`
	GPUEpoch             int64  `json:"gpu_epoch"`
	ResidencySeq         int64  `json:"residency_seq"`
	GPUUUID              string `json:"gpu_uuid"`
	ResidencyState       string `json:"residency_state"`
	HumanProtected       bool   `json:"human_protected"`
	GUIConnectionAlive   bool   `json:"gui_connection_alive"`
	Evictable            bool   `json:"evictable"`
	Dirty                bool   `json:"dirty"`
	SnapshotInProgress   bool   `json:"snapshot_in_progress"`
	SnapshotRequired     bool   `json:"snapshot_required"`
	BlockingReason       string `json:"blocking_reason"`
	PinnedBudgetMiB      int64  `json:"pinned_budget_mib"`
	VRAMAllocatedMiB     *int64 `json:"vram_allocated_mib,omitempty"`
	ProvenVRAMMiB        int64  `json:"proven_vram_mib"`
	ActiveComputeLeaseID string `json:"active_compute_lease_id,omitempty"`
	IdleSeconds          int64  `json:"idle_seconds"`
	TemplateUpgraded     bool   `json:"template_upgraded"`
}

type GPUSchedulerEvent struct {
	EventID      int64  `json:"event_id"`
	NodeUID      string `json:"node_uid"`
	GPUUUID      string `json:"gpu_uuid"`
	GPUIndex     int    `json:"gpu_index"`
	CardEventSeq int64  `json:"card_event_seq"`
	OccurredAt   string `json:"occurred_at"`
	EntityType   string `json:"entity_type"`
	EntityID     string `json:"entity_id"`
	FromState    string `json:"from_state"`
	ToState      string `json:"to_state"`
	BudgetMiB    int64  `json:"budget_mib"`
	ReasonCode   string `json:"reason_code"`
}

type GPUSchedulerSnapshot struct {
	StationID        string                  `json:"station_id"`
	AppGPUQuota      int                     `json:"app_gpu_quota"`
	CutoverCompleted bool                    `json:"cutover_completed"`
	GeneratedAt      string                  `json:"generated_at"`
	Cards            []GPUSchedulerCard      `json:"cards"`
	ActiveLeases     []GPUSchedulerLease     `json:"active_leases"`
	Residencies      []GPUSchedulerResidency `json:"residencies"`
	RecentEvents     []GPUSchedulerEvent     `json:"recent_events"`
}

type GPUSchedulerResponse struct {
	Snapshot            GPUSchedulerSnapshot `json:"snapshot"`
	ViewerCanForceDrain bool                 `json:"viewer_can_force_drain"`
	AllowedActions      []string             `json:"allowed_actions"`
	RequestID           string               `json:"request_id,omitempty"`
}

type EvictGPUResidencyInput struct {
	IdempotencyKey       string `json:"idempotency_key"`
	InstanceID           string `json:"instance_id"`
	ExpectedBootID       string `json:"expected_boot_id,omitempty"`
	ExpectedGPUEpoch     *int64 `json:"expected_gpu_epoch,omitempty"`
	ExpectedResidencySeq *int64 `json:"expected_residency_seq,omitempty"`
}

type ForceDrainGPUResidencyInput struct {
	IdempotencyKey       string `json:"idempotency_key"`
	InstanceID           string `json:"instance_id"`
	ConfirmForceDrain    bool   `json:"confirm_force_drain"`
	ConfirmInstanceID    string `json:"confirm_instance_id"`
	ExpectedBootID       string `json:"expected_boot_id,omitempty"`
	ExpectedGPUEpoch     *int64 `json:"expected_gpu_epoch,omitempty"`
	ExpectedResidencySeq *int64 `json:"expected_residency_seq,omitempty"`
}

type GPUResidencyActionResult struct {
	InstanceID       string `json:"instance_id"`
	AppID            string `json:"app_id"`
	FromState        string `json:"from_state"`
	ResidencyState   string `json:"residency_state"`
	BootID           string `json:"boot_id"`
	GPUEpoch         int64  `json:"gpu_epoch"`
	ResidencySeq     int64  `json:"residency_seq"`
	ReclaimedVRAMMiB int64  `json:"reclaimed_vram_mib"`
	LedgerEventSeq   int64  `json:"ledger_event_seq"`
}

type GPUResidencyActionResponse struct {
	Result    GPUResidencyActionResult `json:"result"`
	RequestID string                   `json:"request_id,omitempty"`
}

type StorageWatermark struct {
	FilesystemTotalBytes        *int64  `json:"filesystem_total_bytes"`
	FilesystemFreeBytes         *int64  `json:"filesystem_free_bytes"`
	CachedReadyBytes            int64   `json:"cached_ready_bytes"`
	UnmaterializedReservedBytes int64   `json:"unmaterialized_reserved_bytes"`
	ReservedHeadroomBytes       int64   `json:"reserved_headroom_bytes"`
	MaxAutoPullModelBytes       int64   `json:"max_auto_pull_model_bytes"`
	HeadroomCompliant           *bool   `json:"headroom_compliant"`
	SampledAt                   *string `json:"sampled_at"`
}

type ModelCacheItem struct {
	CatalogCode      string   `json:"catalog_code"`
	ModelID          string   `json:"model_id"`
	DisplayName      string   `json:"display_name"`
	Category         string   `json:"category"`
	CategoryDir      string   `json:"category_dir"`
	Filename         string   `json:"filename"`
	RelativeViewPath string   `json:"relative_view_path"`
	SHA256           string   `json:"sha256"`
	SizeBytes        int64    `json:"size_bytes"`
	VRAMFootprintMiB int64    `json:"vram_footprint_mib"`
	License          string   `json:"license"`
	LicenseTier      string   `json:"license_tier"`
	PaidAccess       bool     `json:"paid_access"`
	OriginKind       string   `json:"origin_kind"`
	ApprovalState    string   `json:"approval_state"`
	Status           string   `json:"status"`
	DownloadFence    int64    `json:"download_fence"`
	BytesDownloaded  int64    `json:"bytes_downloaded"`
	PartBytesOnDisk  int64    `json:"part_bytes_on_disk"`
	SpeedBps         int64    `json:"speed_bps"`
	ETASeconds       *int     `json:"eta_seconds"`
	VerifiedAt       *string  `json:"verified_at"`
	ErrorCode        string   `json:"error_code,omitempty"`
	ErrorMessage     string   `json:"error_message,omitempty"`
	ActivePins       int      `json:"active_pins"`
	ReservedPins     int      `json:"reserved_pins"`
	PinScopes        []string `json:"pin_scopes"`
}

type ModelsResponse struct {
	SchemaVersion int              `json:"schema_version"`
	MountPath     string           `json:"mount_path"`
	Watermark     StorageWatermark `json:"watermark"`
	Models        []ModelCacheItem `json:"models"`
	TotalCount    int              `json:"total_count"`
	Truncated     bool             `json:"truncated,omitempty"`
	RequestID     string           `json:"request_id,omitempty"`
}

type EvictModelInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	SHA256         string `json:"sha256"`
}

type EvictModelResponse struct {
	Status    string          `json:"status"`
	SHA256    string          `json:"sha256"`
	Model     *ModelCacheItem `json:"model,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
}

