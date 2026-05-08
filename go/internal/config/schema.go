package config

// Schema is the top-level configuration loaded from WORKFLOW.md front matter.
type Schema struct {
	Tracker   TrackerConfig   `yaml:"tracker"`
	Polling   PollingConfig   `yaml:"polling"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Hooks     HooksConfig     `yaml:"hooks"`
	Agent     AgentConfig     `yaml:"agent"`
	Worker    WorkerConfig    `yaml:"worker"`
	HA        HAConfig        `yaml:"ha"`
	Server    ServerConfig    `yaml:"server"`
}

type TrackerConfig struct {
	Kind           string        `yaml:"kind"`
	APIKey         string        `yaml:"api_key"`
	ActiveStates   []string      `yaml:"active_states"`
	TerminalStates []string      `yaml:"terminal_states"`
	Linear         LinearConfig  `yaml:"linear"`
	Plane          PlaneConfig   `yaml:"plane"`
}

type LinearConfig struct {
	ProjectSlug string `yaml:"project_slug"`
	Endpoint    string `yaml:"endpoint"`
}

type PlaneConfig struct {
	WorkspaceSlug string `yaml:"workspace_slug"`
	ProjectID     string `yaml:"project_id"`
	Endpoint      string `yaml:"endpoint"`
}

type PollingConfig struct {
	IntervalMS int `yaml:"interval_ms"`
}

type WorkspaceConfig struct {
	Root string `yaml:"root"`
}

type HooksConfig struct {
	AfterCreate  string `yaml:"after_create"`
	BeforeRun    string `yaml:"before_run"`
	AfterRun     string `yaml:"after_run"`
	BeforeRemove string `yaml:"before_remove"`
	TimeoutMS    int    `yaml:"timeout_ms"`
}

type AgentConfig struct {
	Kind                 string         `yaml:"kind"`
	MaxConcurrent        int            `yaml:"max_concurrent"`
	MaxTurns             int            `yaml:"max_turns"`
	MaxRetryBackoffMS    int            `yaml:"max_retry_backoff_ms"`
	MaxConcurrentByState map[string]int `yaml:"max_concurrent_by_state"`
	Codex                CodexConfig    `yaml:"codex"`
	Claude               ClaudeConfig   `yaml:"claude"`
}

type CodexConfig struct {
	Command           string `yaml:"command"`
	ApprovalPolicy    string `yaml:"approval_policy"`
	ThreadSandbox     string `yaml:"thread_sandbox"`
	TurnSandboxPolicy string `yaml:"turn_sandbox_policy"`
	TurnTimeoutMS     int    `yaml:"turn_timeout_ms"`
	ReadTimeoutMS     int    `yaml:"read_timeout_ms"`
	StallTimeoutMS    int    `yaml:"stall_timeout_ms"`
}

type ClaudeConfig struct {
	Command        string   `yaml:"command"`
	PermissionMode string   `yaml:"permission_mode"`
	AllowedTools   []string `yaml:"allowed_tools"`
	MaxTurns       int      `yaml:"max_turns"`
}

type WorkerConfig struct {
	SSHHosts []string `yaml:"ssh_hosts"`
}

type HAConfig struct {
	Enabled       bool     `yaml:"enabled"`
	EtcdEndpoints []string `yaml:"etcd_endpoints"`
	LeaseTTLMS    int      `yaml:"lease_ttl_ms"`
	AdvertiseAddr string   `yaml:"advertise_addr"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}
