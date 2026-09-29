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

func TestParseKeyIDs(t *testing.T) {
	ok := map[string][]int64{
		"":         nil,
		"7":        {7},
		"7,9":      {7, 9},
		" 7 , 9 ,": {7, 9},
	}
	for in, want := range ok {
		got, err := parseKeyIDs(in)
		if err != nil || len(got) != len(want) {
			t.Errorf("parseKeyIDs(%q) = %v, %v, want %v", in, got, err, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("parseKeyIDs(%q) = %v, want %v", in, got, want)
			}
		}
	}
	for _, in := range []string{"abc", "7,x", "0", "-1"} {
		if _, err := parseKeyIDs(in); err == nil {
			t.Errorf("parseKeyIDs(%q) expected error", in)
		}
	}
}
