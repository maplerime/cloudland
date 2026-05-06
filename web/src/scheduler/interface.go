/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import "context"

// Filter eliminates unsuitable hosts from the candidate list.
type Filter interface {
	Name() string
	Filter(ctx context.Context, req *PlacementRequest, hosts []*HostState) []*HostState
}

// Weigher assigns a raw score to a single host.
// The scheduler normalizes scores across all candidates and multiplies by Multiplier().
type Weigher interface {
	Name() string
	Multiplier() float64
	Score(req *PlacementRequest, host *HostState) float64
}
