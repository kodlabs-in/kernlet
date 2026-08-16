package guestproto

const Port uint32 = 10789

type Request struct {
	ID       uint64   `json:"id"`
	Method   string   `json:"method"`
	Args     []string `json:"args,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	Rootfs   string   `json:"rootfs,omitempty"`
	UID      uint32   `json:"uid,omitempty"`
	GID      uint32   `json:"gid,omitempty"`

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
