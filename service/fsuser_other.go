//go:build !linux

package main

import "errors"

// asFSUser needs Linux's per-thread filesystem ids; elsewhere it refuses instead of
// running fn as the current user.
func asFSUser(uid, gid int, fn func()) error {
	return errors.New("switching the filesystem user requires linux")
}
