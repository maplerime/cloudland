/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"errors"
	"testing"

	. "web/src/common"
	"web/src/model"
)

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

func updateKeysErrCode(t *testing.T, action, user string, keys []*model.Key) ErrCode {
	t.Helper()
	_, err := instanceAdmin.UpdateKeys(context.Background(), &model.Instance{}, action, user, keys)
	var clErr *CLError
	if !errors.As(err, &clErr) {
		t.Fatalf("UpdateKeys(%s, %q, %d keys) error = %v, want CLError", action, user, len(keys), err)
	}
	return clErr.Code
}

// input validation runs before the permission check and needs no DB
func TestUpdateKeys_ValidationErrorCodes(t *testing.T) {
	key := []*model.Key{{PublicKey: "ssh-ed25519 AAAA a@b"}}
	if c := updateKeysErrCode(t, "add", "", nil); c != ErrSSHKeyRequired {
		t.Errorf("add without keys: code %v, want %v", c, ErrSSHKeyRequired)
	}
	if c := updateKeysErrCode(t, "remove", "", nil); c != ErrSSHKeyRequired {
		t.Errorf("remove without keys: code %v, want %v", c, ErrSSHKeyRequired)
	}
	if c := updateKeysErrCode(t, "add", "Bad User", key); c != ErrSSHKeyInvalidGuestUser {
		t.Errorf("invalid user: code %v, want %v", c, ErrSSHKeyInvalidGuestUser)
	}
	if c := updateKeysErrCode(t, "replace", "root", key); c != ErrInvalidParameter {
		t.Errorf("invalid action: code %v, want %v", c, ErrInvalidParameter)
	}
}
