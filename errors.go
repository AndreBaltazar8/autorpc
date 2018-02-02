package autorpc

import (
	"fmt"
)

// An RPCError describes an error in the RPC service
type RPCError struct {
	Err       string
	ActualErr error
}

func (e *RPCError) Error() string {
	if e.Err == "" {
		return fmt.Sprintf("autorpc: internal error %s", e.ActualErr)
	}
	return fmt.Sprintf("autorpc: %s", e.Err)
}
