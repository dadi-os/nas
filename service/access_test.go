package main

import "testing"

func TestValidateSSHPassword(t *testing.T) {
	if err := validateSSHPassword("short"); err == nil {
		t.Fatal("expected error for short password")
	}
	if err := validateSSHPassword("long-enough-secret"); err != nil {
		t.Fatalf("valid password: %v", err)
	}
	if err := validateSSHPassword("has\nnewline-xx"); err == nil {
		t.Fatal("expected error for newline")
	}
}

func TestPasswordSetFromPasswdS(t *testing.T) {
	if !passwordSetFromPasswdS("ankur P 2026-09-14 0 99999 7 -1") {
		t.Fatal("P should count as set")
	}
	if passwordSetFromPasswdS("ankur L 2026-09-14 0 99999 7 -1") {
		t.Fatal("locked should not count as set")
	}
	if passwordSetFromPasswdS("ankur NP 2026-09-14 0 99999 7 -1") {
		t.Fatal("no password should not count as set")
	}
	if passwordSetFromPasswdS("") {
		t.Fatal("empty should not count as set")
	}
}
