package main

const (
	apiSchema  = "chswap.api.v1"
	apiVersion = "1"
	moduleID   = "crystal_hybridswap_webui"
)

const unsupportedMessage = "kernel does not expose the required Crystal Hybridswap management nodes"
const unsupportedCode = "E_KERNEL_NODE_MISSING"

type App struct {
	SysRoot        string
	ProcRoot       string
	DevRoot        string
	MemcgRoot      string
	ModDir         string
	DataDir        string
	StateDir       string
	LogDir         string
	DiagnosticsDir string
	KSUD           string
	SkipKSUD       bool
	DeadlineMS     int64
}

type Envelope struct {
	OK       bool      `json:"ok"`
	Schema   string    `json:"schema"`
	Data     any       `json:"data,omitempty"`
	Error    *APIError `json:"error,omitempty"`
	Warnings []Warning `json:"warnings"`
}

type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type Warning struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type FrontendGate struct {
	Allowed    bool   `json:"allowed"`
	ReasonCode string `json:"reason_code,omitempty"`
	Message    string `json:"message,omitempty"`
}

type Capabilities struct {
	BackendVersion string         `json:"backend_version"`
	APIVersion     string         `json:"api_version"`
	FrontendGate   FrontendGate   `json:"frontend_gate"`
	Kernel         KernelCaps     `json:"kernel"`
	Zram           ZramCaps       `json:"zram"`
	ProfileStorage ProfileStorage `json:"profile_storage"`
}

// ZramCaps advertises what the running kernel actually supports so the UI can
// build a compression-algorithm dropdown and a disksize slider bounded by RAM.
type ZramCaps struct {
	CompAlgorithms      []string `json:"comp_algorithms"`
	CompAlgorithmActive string   `json:"comp_algorithm_active,omitempty"`
	MemTotalBytes       int64    `json:"mem_total_bytes"`
}

type KernelCaps struct {
	CrystalPresent       bool              `json:"crystal_present"`
	ZramControlPresent   bool              `json:"zram_control_present"`
	RequiredNodesMissing []string          `json:"required_nodes_missing,omitempty"`
	CrystalReport        map[string]string `json:"crystal_report,omitempty"`
}

type ProfileStorage struct {
	Backend string `json:"backend"`
	Path    string `json:"path,omitempty"`
}

type Status struct {
	Mode            string        `json:"mode"`
	FrontendGate    FrontendGate  `json:"frontend_gate"`
	Degraded        bool          `json:"degraded"`
	DegradedReasons []string      `json:"degraded_reasons"`
	Zram            ZramStatus    `json:"zram"`
	Swap            SwapStatus    `json:"swap"`
	Crystal         CrystalStatus `json:"crystal"`
	ZMS             ZmsStatus     `json:"zms"`
	Policy          PolicyStatus  `json:"policy"`
	Boot            BootStatus    `json:"boot"`
	Storage         StorageStatus `json:"storage"`
}

type ZramStatus struct {
	Device          string         `json:"device"`
	Found           bool           `json:"found"`
	Initstate       int64          `json:"initstate"`
	ActiveSwap      bool           `json:"active_swap"`
	DisksizeBytes   int64          `json:"disksize_bytes,omitempty"`
	MemLimitBytes   int64          `json:"mem_limit_bytes,omitempty"`
	CompAlgorithm   string         `json:"comp_algorithm,omitempty"`
	RecompAlgorithm string         `json:"recomp_algorithm,omitempty"`
	BackingDev      string         `json:"backing_dev,omitempty"`
	BackingBoundVia string         `json:"backing_bound_via,omitempty"`
	MMStat          map[string]any `json:"mm_stat,omitempty"`
}

type SwapStatus struct {
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	Priority  int64  `json:"priority"`
	Device    string `json:"device,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	UsedBytes int64  `json:"used_bytes"`
}

type CrystalStatus struct {
	Enable         int64 `json:"enable"`
	CoreEnable     int64 `json:"core_enable"`
	SwapdPause     int64 `json:"swapd_pause"`
	Loglevel       int64 `json:"loglevel"`
	DevLife        int64 `json:"dev_life"`
	QuotaDay       int64 `json:"quota_day"`
	QuotaUsed      int64 `json:"quota_used"`
	QuotaRemaining int64 `json:"quota_remaining"`
	ZramIncrease   int64 `json:"zram_increase"`
}

type ZmsStatus struct {
	Enabled int64 `json:"enabled"`
	Ret     int64 `json:"ret,omitempty"`
	// Present indicates the zms_stat node was found & parsed. When false the
	// numeric fields below are meaningless (omitted) rather than a real zero.
	Present bool `json:"present"`
	// Stat counters: emitted even when zero so the UI can distinguish a real
	// "0 blocks used" from "no data" (which shows as present=false).
	NrBlocks             int64   `json:"nr_blocks"`
	UsedBlocks           int64   `json:"used_blocks"`
	Objects              int64   `json:"objects"`
	OriginalBytes        int64   `json:"original_bytes"`
	StoredBytes          int64   `json:"stored_bytes"`
	PackedBytes          int64   `json:"packed_bytes"`
	SlotOverheadBytes    int64   `json:"slot_overhead_bytes"`
	SlotOverheadPct      float64 `json:"slot_overhead_pct"`
	BlockBytes           int64   `json:"block_bytes"`
	BlockWasteBytes      int64   `json:"block_waste_bytes"`
	BlockUtilizationPct  float64 `json:"block_utilization_pct"`
	BackingSavingsBytes  int64   `json:"backing_savings_bytes"`
	BackingSavingsPct    float64 `json:"backing_savings_pct"`
	SpaceDeltaBytes      int64   `json:"space_delta_bytes"`
	SpaceSavedPct        float64 `json:"space_saved_pct"`
	PackingEfficiencyPct float64 `json:"packing_efficiency_pct"`
}

type PolicyStatus struct {
	ZramWmRatio          int64          `json:"zram_wm_ratio"`
	AvailBuffers         AvailBuffers   `json:"avail_buffers"`
	ErmAvailBufferEnable int64          `json:"erm_avail_buffer_enable"`
	ErmAvailBuffer       ErmAvailBuffer `json:"erm_avail_buffer"`
}

type StorageStatus struct {
	ProfileBackend string `json:"profile_backend"`
	ProfilePath    string `json:"profile_path,omitempty"`
	DataDir        string `json:"data_dir,omitempty"`
}

type BootStatus struct {
	Phase           string   `json:"phase"`
	Result          string   `json:"result"`
	StartedAtMS     int64    `json:"started_at_ms"`
	FinishedAtMS    int64    `json:"finished_at_ms"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	DegradedReasons []string `json:"degraded_reasons,omitempty"`
	LastErrorCode   string   `json:"last_error_code,omitempty"`
	LogPath         string   `json:"log_path,omitempty"`
	LogTail         []string `json:"log_tail"`
}

type ValidationResult struct {
	Valid             bool     `json:"valid"`
	NormalizedProfile *Profile `json:"normalized_profile,omitempty"`
}

type ApplyRequest struct {
	Mode    string   `json:"mode"`
	Profile *Profile `json:"profile,omitempty"`
}

type BootApplyRequest struct {
	Phase     string `json:"phase"`
	TimeoutMS int64  `json:"timeout_ms,omitempty"`
}

type ApplyResult struct {
	Changed         bool        `json:"changed"`
	Degraded        bool        `json:"degraded"`
	DegradedReasons []string    `json:"degraded_reasons,omitempty"`
	Steps           []ApplyStep `json:"steps"`
	Boot            *BootStatus `json:"boot,omitempty"`
}

type ApplyStep struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Target    string `json:"target,omitempty"`
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	OldValue  any    `json:"old_value,omitempty"`
	NewValue  any    `json:"new_value,omitempty"`
}

type DiagnosticsResult struct {
	Path string `json:"path"`
}

type Profile struct {
	Schema  string         `json:"schema"`
	Mode    string         `json:"mode"`
	Boot    BootProfile    `json:"boot"`
	Zram    ZramProfile    `json:"zram"`
	Backing BackingProfile `json:"backing"`
	Swap    SwapProfile    `json:"swap"`
	Crystal CrystalProfile `json:"crystal"`
	Policy  PolicyProfile  `json:"policy"`
}

type BootProfile struct {
	PostFSTimeoutMS int64 `json:"post_fs_timeout_ms"`
}

// Three-state config: a nil pointer means "unset — follow the system default".
// The daemon only reconciles fields the user has explicitly set.
type ZramProfile struct {
	Device        string  `json:"device"`         // fixed managed device (zram0)
	Disksize      *string `json:"disksize"`       // nil=follow system (daemon default=RAM)
	CompAlgorithm *string `json:"comp_algorithm"` // nil=kernel default
}

// Backing store selection. block => fixed raw hybridswap partition;
// loop => module-managed writeback file mounted as a loop device.
type BackingProfile struct {
	Mode          string `json:"mode"`                      // none | block | loop
	LoopSizeBytes *int64 `json:"loop_size_bytes,omitempty"` // loop file size (default 1G)
}

type SwapProfile struct {
	Enabled  bool  `json:"enabled"`
	Priority int64 `json:"priority"`
}

type CrystalProfile struct {
	Enable       *int64 `json:"enable"`
	CoreEnable   *int64 `json:"core_enable"`
	SwapdPause   *int64 `json:"swapd_pause"`
	Loglevel     *int64 `json:"loglevel"`
	DevLife      *int64 `json:"dev_life"`
	QuotaDay     *int64 `json:"quota_day"`
	ZramIncrease *int64 `json:"zram_increase"`
}

type PolicyProfile struct {
	ZramWmRatio  *int64        `json:"zram_wm_ratio"`
	AvailBuffers *AvailBuffers `json:"avail_buffers"`
	// erm_avail_buffer_enable = "允许 ERM 覆盖策略"; min/high are NOT managed.
	ErmAvailBufferEnable *int64 `json:"erm_avail_buffer_enable"`
}

type AvailBuffers struct {
	AvailMB             int64 `json:"avail_mb"`
	MinMB               int64 `json:"min_mb"`
	HighMB              int64 `json:"high_mb"`
	FreeSwapThresholdMB int64 `json:"free_swap_threshold_mb"`
}

type ErmAvailBuffer struct {
	MinMB  int64 `json:"min_mb"`
	HighMB int64 `json:"high_mb"`
}

// backingBlockDevice is the fixed default block backing device (not user-editable).
//
// Use the raw by-name partition instead of OPlus' later-created dm-crypt mapper
// so full-control can bind during post-fs-data.
const backingBlockDevice = "/dev/block/by-name/hybridswap"
