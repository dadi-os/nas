//go:build linux

package main

import (
	"fmt"
	"runtime"
	"syscall"
)

// asFSUser runs fn on a dedicated OS thread whose filesystem uid and gid are uid and gid,
// so the kernel checks permissions and owns everything fn creates as that user instead of
// root. The thread stays locked and exits with the goroutine, so the switched identity
// never reaches other work. A panic in fn is raised again in the caller.
func asFSUser(uid, gid int, fn func()) error {
	type outcome struct {
		err   error
		panic any
	}
	done := make(chan outcome, 1)
	go func() {
		runtime.LockOSThread()
		if err := setFSID(syscall.SYS_SETFSGID, gid); err != nil {
			done <- outcome{err: err}
			return
		}
		if err := setFSID(syscall.SYS_SETFSUID, uid); err != nil {
			done <- outcome{err: err}
			return
		}
		defer func() {
			if p := recover(); p != nil {
				done <- outcome{panic: p}
			}
		}()
		fn()
		done <- outcome{}
	}()
	o := <-done
	if o.panic != nil {
		panic(o.panic)
	}
	return o.err
}

// setFSID calls setfsuid or setfsgid (trap) for id. Neither reports failure, only the
// previous id, so a second call with -1 reads the current id back to confirm the switch.
func setFSID(trap uintptr, id int) error {
	syscall.RawSyscall(trap, uintptr(id), 0, 0)
	current, _, _ := syscall.RawSyscall(trap, ^uintptr(0), 0, 0)
	if int(uint32(current)) != id {
		return fmt.Errorf("switch filesystem id to %d: still %d", id, int(uint32(current)))
	}
	return nil
}
