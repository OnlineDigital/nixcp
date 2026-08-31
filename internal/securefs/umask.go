// Package securefs provides narrowly scoped process-wide filesystem safety
// controls. umask is process-global, so calls are serialized and always
// restored before returning.
package securefs

import (
	"sync"
	"syscall"
)

var umaskMu sync.Mutex

// WithPrivateUmask runs fn with umask 077, restoring the caller's umask even
// when fn returns an error. Use it around creation of private state only.
func WithPrivateUmask(fn func() error) error {
	umaskMu.Lock()
	defer umaskMu.Unlock()
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	return fn()
}
