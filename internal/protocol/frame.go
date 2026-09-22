package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	TypeCall   = "call"
	TypeResult = "result"
	TypePing   = "ping"
	TypePong   = "pong"
)

const MaxFrameBytes int64 = 16 << 20

type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Frame struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Tool   string          `json:"tool,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	OK     bool            `json:"ok,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RemoteError    `json:"error,omitempty"`
}

func (f Frame) Validate() error {
	switch f.Type {
	case TypeCall:
		if f.ID == "" || f.Tool == "" {
			return errors.New("call frame requires id and tool")
		}
		if len(f.Args) == 0 {
			return errors.New("call frame requires args")
		}
	case TypeResult:
		if f.ID == "" {
			return errors.New("result frame requires id")
		}
		if !f.OK && f.Error == nil {
			return errors.New("failed result requires error")
		}
	case TypePing, TypePong:
		// no additional fields required
	default:
		return fmt.Errorf("unknown frame type %q", f.Type)
	}
	return nil
}
