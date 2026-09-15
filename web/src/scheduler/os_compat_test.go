/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import "testing"

func TestParseOSVersion(t *testing.T) {
	cases := []struct {
		os     string
		want   int
		wantOK bool
	}{
		{"Ubuntu 22.04.3 LTS", 2204, true},
		{"Ubuntu 26.04 LTS", 2604, true},
		{"Ubuntu 22.10", 2210, true},
		{"", 0, false},
		{"CentOS Linux 7 (Core)", 0, false},
		{"Debian GNU/Linux 12 (bookworm)", 0, false},
	}
	for _, c := range cases {
		got, ok := parseOSVersion(c.os)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("parseOSVersion(%q) = (%d, %v), want (%d, %v)", c.os, got, ok, c.want, c.wantOK)
		}
	}
}

func TestOSMigrationAllowed(t *testing.T) {
	cases := []struct {
		source string
		target string
		want   bool
	}{
		{"Ubuntu 22.04.3 LTS", "Ubuntu 26.04 LTS", true},    // upgrade allowed
		{"Ubuntu 22.04.3 LTS", "Ubuntu 22.04.3 LTS", true},  // same version allowed
		{"Ubuntu 26.04 LTS", "Ubuntu 22.04.3 LTS", false},   // downgrade blocked
		{"Ubuntu 22.10", "Ubuntu 22.04 LTS", false},         // minor downgrade blocked
		{"Ubuntu 26.04 LTS", "CentOS Linux 7 (Core)", true}, // unparseable target -> allowed
		{"CentOS Linux 7 (Core)", "Ubuntu 22.04 LTS", true}, // unparseable source -> allowed
	}
	for _, c := range cases {
		if got := OSMigrationAllowed(c.source, c.target); got != c.want {
			t.Errorf("OSMigrationAllowed(%q, %q) = %v, want %v", c.source, c.target, got, c.want)
		}
	}
}

func TestFilterOSCompatible(t *testing.T) {
	hosts := []*HostState{
		{HyperID: 1, OS: "Ubuntu 22.04.3 LTS"}, // same version
		{HyperID: 2, OS: "Ubuntu 26.04 LTS"},   // newer -> upgrade allowed
		{HyperID: 3, OS: "Ubuntu 20.04 LTS"},   // older -> downgrade blocked
		{HyperID: 4, OS: ""},                   // unknown -> kept (best-effort)
	}

	// Source 22.04: keep same(1), newer(2), unknown(4); drop older(3).
	got := filterOSCompatible(hosts, "Ubuntu 22.04.3 LTS")
	if !hasHyper(got, 1) || !hasHyper(got, 2) || !hasHyper(got, 4) || hasHyper(got, 3) {
		t.Errorf("source 22.04: got hypers %v, expected {1,2,4} without 3", ids(got))
	}

	// Source 26.04: only 26.04(2) and unknown(4) remain; 22.04 and 20.04 are downgrades.
	got = filterOSCompatible(hosts, "Ubuntu 26.04 LTS")
	if !hasHyper(got, 2) || !hasHyper(got, 4) || hasHyper(got, 1) || hasHyper(got, 3) {
		t.Errorf("source 26.04: got hypers %v, expected {2,4} only", ids(got))
	}

	// Unparseable source OS: keep everything (never block on unknown source).
	got = filterOSCompatible(hosts, "CentOS Linux 7 (Core)")
	if len(got) != len(hosts) {
		t.Errorf("unparseable source: got %d hosts, want %d", len(got), len(hosts))
	}
}

func hasHyper(hosts []*HostState, id int32) bool {
	for _, h := range hosts {
		if h.HyperID == id {
			return true
		}
	}
	return false
}

func ids(hosts []*HostState) []int32 {
	out := make([]int32, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, h.HyperID)
	}
	return out
}
