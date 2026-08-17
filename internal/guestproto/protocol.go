package guestproto

const (
	Port             uint32 = 10789
	NetworkCheckPort        = 10790

	NetworkCheckRequest  = "kernlet-network-check"
	NetworkCheckResponse = "kernlet-network-ready"
)

type Request struct {
	ID       uint64   `json:"id"`
	Method   string   `json:"method"`
	Args     []string `json:"args,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	Image    string   `json:"image,omitempty"`

	MemoryMax uint64 `json:"memory_max,omitempty"`
	PidsMax   uint64 `json:"pids_max,omitempty"`
	CPUQuota  uint64 `json:"cpu_quota,omitempty"`
	CPUPeriod uint64 `json:"cpu_period,omitempty"`
}

type Response struct {
	ID      uint64 `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}
