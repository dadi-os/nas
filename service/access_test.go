package main

import (
	"errors"
	"testing"
)

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

func TestValidateSSHUsername(t *testing.T) {
	if err := validateSSHUsername("ab"); err != nil {
		t.Fatalf("valid username: %v", err)
	}
	if err := validateSSHUsername("a"); err == nil {
		t.Fatal("expected error for one-character name")
	}
	if err := validateSSHUsername("Root"); err == nil {
		t.Fatal("expected error for uppercase")
	}
	if err := validateSSHUsername("dadi"); !errors.Is(err, errSSHReserved) {
		t.Fatalf("dadi: %v", err)
	}
	if err := validateSSHUsername("root"); !errors.Is(err, errSSHReserved) {
		t.Fatalf("root: %v", err)
	}
	if err := validateSSHUsername("systemd-foo"); !errors.Is(err, errSSHReserved) {
		t.Fatalf("systemd: %v", err)
	}
}

func TestPasswordSetFromPasswdS(t *testing.T) {
	if !passwordSetFromPasswdS("alice P 2026-09-14 0 99999 7 -1") {
		t.Fatal("P should count as set")
	}
	if passwordSetFromPasswdS("alice L 2026-09-14 0 99999 7 -1") {
		t.Fatal("locked should not count as set")
	}
	if passwordSetFromPasswdS("alice NP 2026-09-14 0 99999 7 -1") {
		t.Fatal("no password should not count as set")
	}
	if passwordSetFromPasswdS("") {
		t.Fatal("empty should not count as set")
	}
}

func TestSSHAdminNamesFromPasswd(t *testing.T) {
	out := "" +
		"root:x:0:0:root:/root:/bin/bash\n" +
		"dadi:x:1000:1000:dadi:/var/lib/dadi:/bin/bash\n" +
		"alice:x:1001:1001:alice:/home/alice:/bin/bash\n" +
		"bob:x:1002:1002:bob:/home/bob:/bin/bash\n" +
		"nobody:x:65534:65534:nobody:/:/sbin/nologin\n"
	got := sshAdminNamesFromPasswd(out)
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Fatalf("got %v", got)
	}
}

func TestAllowUsersFileBody(t *testing.T) {
	body := allowUsersFileBody([]string{"bob", "alice"})
	if body != "AllowUsers alice bob\n" {
		t.Fatalf("body %q", body)
	}
}
