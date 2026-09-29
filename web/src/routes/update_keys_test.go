/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package routes

import "testing"

func TestFirstKeyLine(t *testing.T) {
	cases := map[string]string{
		"ssh-ed25519 AAAA a@b":                        "ssh-ed25519 AAAA a@b",
		"  ssh-ed25519 AAAA a@b  \n":                  "ssh-ed25519 AAAA a@b",
		"\n\nssh-rsa BBBB x\nssh-rsa EVIL attacker\n": "ssh-rsa BBBB x",
		"ssh-rsa BBBB x\r\nssh-rsa EVIL attacker":     "ssh-rsa BBBB x",
		"": "",
	}
	for in, want := range cases {
		if got := firstKeyLine(in); got != want {
			t.Errorf("firstKeyLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGuestUserPattern(t *testing.T) {
	for _, u := range []string{"root", "ubuntu", "_svc", "deploy-1", "a"} {
		if !guestUserPattern.MatchString(u) {
			t.Errorf("expected %q to be valid", u)
		}
	}
	for _, u := range []string{"", "Root", "a b", "x';rm -rf /", "1user", "abcdefghijklmnopqrstuvwxyzabcdefg"} {
		if guestUserPattern.MatchString(u) {
			t.Errorf("expected %q to be invalid", u)
		}
	}
}
