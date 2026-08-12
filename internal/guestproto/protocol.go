package guestproto

const Port uint32 = 10789

type Request struct {
	ID     uint64   `json:"id"`
	Method string   `json:"method"`
	Args   []string `json:"args,omitempty"`
}

type Response struct {
	ID      uint64 `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}
