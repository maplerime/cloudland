/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var blockedIPAdmin = &BlockedIPAdmin{}

const (
	// Blocking lasts an hour, so a day of history is what an operator actually
	// scans; the ceiling is the Prometheus retention (720h).
	BlockedIPHistoryWindow = 24 * time.Hour
	blockedIPMaxWindow     = 720 * time.Hour

	// How long block_ip.sh keeps an entry in the blacklist. The exporter reads
	// the block_src/block_dst info sets, which only live 300s, so the metric
	// marks when a block *started* rather than how long it lasts -- the duration
	// is this constant. Direction comes for free, since only those sets carry it.
	blockedIPBlockingTimeout = 3600

	// A block that started up to blockedIPBlockingTimeout ago may still be in
	// force, so the current view looks that far back; the margin covers the
	// delay between the block and its first sample.
	blockedIPCurrentWindow = blockedIPBlockingTimeout + 300
	blockedIPCurrentStep   = 30

	// 300s separates two blocks of the same IP while keeping the number of
	// points per series bounded. It is the ceiling of the history grid, not a
	// fixed value -- see blockedIPStep.
	blockedIPHistoryStep = 300

	// Every window is cut into at least this many points, so that a block that
	// only lives 300s cannot fall between two of them.
	blockedIPMinPoints = 10

	// A rejection reason is a diagnostic, not a payload; read enough of it to be
	// useful and no more.
	blockedIPErrorBodyLimit = 4096

	// Decoding one Prometheus point into [][]interface{} costs about 100 bytes of
	// heap (measured), so ten million points is roughly a gigabyte. The ceiling is
	// what one query may spend, derived from what this process can hold rather
	// than from how much traffic we have seen: attacks are bursty and a botnet
	// rotating addresses decides the cardinality, so any figure extrapolated from
	// past volume fails exactly when it is needed. Past this the window is
	// narrowed rather than the process risked.
	blockedIPMaxPoints = 10000000

	// Points scale linearly with the window at a fixed step, so the first
	// narrowing already lands within budget. The second pass exists only because
	// a shorter window can match fewer series and thus leave room unused.
	blockedIPFitAttempts = 2

	// Each narrowing pass aims a little under budget, so a denser recent stretch
	// does not immediately put it back over and burn another of the few attempts.
	blockedIPFitMargin = 0.9

	// The floor a window is never narrowed past. Even a node saturated to its
	// conntrack limit can only hold ~174 addresses over the detection threshold
	// at once, so five minutes stays far inside the budget on any fleet size.
	blockedIPMinFitWindow = 5 * time.Minute

	blockedIPMetric = "ipset_blocked_ips"

	// Ownership comes from the control plane's own view of which address it
	// owns, published by petacloud-exporter. The traffic metric this used to
	// walk (domain_north_south_inbound_bytes_total -> domain -> vm_instance_map)
	// only exists while a VM is running, so a reserved address, a detached
	// floating IP, a load balancer VIP and a powered off VM all resolved to
	// nothing there and then read as somebody else's address -- the opposite of
	// the truth. This metric carries the pair directly and does not care whether
	// anything is running.
	blockedIPInstanceMapMetric = "cloudland_ip_instance_map"

	// Whether that mapping was actually being published. Without it a failed
	// scrape is indistinguishable from "this address is not ours", which is the
	// same mistake one level up.
	blockedIPMapHealthMetric = "cloudland_ip_instance_map_scrape_success"

	// Set by the API on every candidate of an address it could not resolve to a
	// single instance.
	blockedIPMapStatusConflict = "conflict"
)

// BlockedIPAdmin answers "which IPs is this region currently dropping".
//
// Nothing is stored locally and nothing is fetched from the compute nodes on
// demand: each node exports its ipset as node_exporter textfile metrics and
// Prometheus scrapes them, so both the current state and the history are PromQL
// queries. Ownership is resolved from metrics as well.
type BlockedIPAdmin struct{}

// BlockedIP is one blocking of an IP on one compute node.
type BlockedIP struct {
	IP           string
	BlockType    string
	Direction    string
	Hostname     string
	InstanceUUID string
	// Load balancer holding the address, set only where InstanceUUID is empty.
	// Carried through to the page because it is the only actionable fact on
	// such a row: there is no VM to look for, and this names what to look at
	// instead.
	LbID string
	// Whether the address belongs to this region at all. Separate from
	// InstanceUUID because an address can be ours while holding no VM -- a
	// reserved address or a load balancer VIP -- and that still decides the
	// direction.
	Ours   bool
	Source string // metric | none
	// "" when the mapping answered cleanly. conflict when the address maps to
	// more than one instance, so the instance beside it must not be acted on.
	// unavailable when the mapping was not being published across the window, so
	// a miss says nothing either way.
	OwnerState string
	BlockedAt  time.Time
	ExpiresAt  time.Time
}

// BlockedIPWindow records what a request was asked for and what it could
// actually afford to answer with.
//
// Every range query the request makes narrows on its own, so this accumulates
// the narrowest of them: Start ends up as the latest start any single query was
// pushed to. Rows outside that are simply absent -- an episode whose ownership
// query got narrowed past it, for instance, reports NA rather than an instance.
type BlockedIPWindow struct {
	Requested time.Time // the start originally asked for
	Start     time.Time // narrowest start actually served across every query
	End       time.Time
	Points    int64 // points behind the window as asked for, which is what overflowed
	Truncated bool  // at least one query was narrowed
}

// narrowedTo records one query having been pushed to a later start. Keeping the
// latest means the reported window never claims more coverage than the request
// actually achieved.
func (w *BlockedIPWindow) narrowedTo(start time.Time, points int64) {
	if w == nil {
		return
	}
	if points > w.Points {
		w.Points = points
	}
	if start.After(w.Start) {
		w.Start = start
		w.Truncated = true
	}
}

// BlockedIPFilter narrows the PromQL selector; every field is optional.
type BlockedIPFilter struct {
	IP        string
	Hostname  string
	BlockType string
}

// BlockedIPOwner is what an ownership lookup yields; every field is empty when
// the IP belongs to no instance, which is normal for external attackers.
type BlockedIPOwner struct {
	InstanceUUID string
	// Load balancer holding the address. Only ever set where InstanceUUID is
	// empty, and the one thing that makes such an address ours: a VIP serves
	// real traffic, so it can both receive a flood and originate connections to
	// its backends.
	LbID   string
	Source string // always "metric"; lets the caller tell a hit from a miss
	State  string // "" resolved cleanly, conflict when several instances claim the address
}

// Ours reports whether the address belongs to this region in a sense worth
// acting on.
//
// An address in the pool that holds neither an instance nor a load balancer is
// deliberately not ours here. Nothing is behind it to send a packet, so traffic
// appearing to come from it is forged, and nothing can be isolated or repaired
// when traffic is aimed at it. Calling it ours would put a row on the page that
// names no owner and affords no action.
func (o *BlockedIPOwner) Ours() bool {
	return o.InstanceUUID != "" || o.LbID != ""
}

type blockedIPEpisode struct {
	IP        string
	Hostname  string
	BlockType string
	FirstSeen int64
	LastSeen  int64
}

type blockedIPPromResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`  // instant queries
			Values [][]interface{}   `json:"values"` // range queries
		} `json:"result"`
	} `json:"data"`
}

// ListCurrent returns the IPs being dropped right now across the region.
//
// The window is fixed and mechanically bounded, so unlike the history it is not
// probed: an address only enters the set after passing the detection threshold,
// and conntrack cannot hold enough connections for more than a few hundred
// addresses per node to do that at once. Narrowing this window would also be
// the wrong answer -- it would hide blocks that are still in force.
func (a *BlockedIPAdmin) ListCurrent(filter *BlockedIPFilter) (blocked []*BlockedIP, window *BlockedIPWindow, err error) {
	now := time.Now().UTC()
	start := now.Add(-blockedIPCurrentWindow * time.Second)
	window = &BlockedIPWindow{Requested: start, Start: start, End: now}
	episodes, err := a.queryEpisodes(filter, start, now, blockedIPCurrentStep, window)
	if err != nil {
		return
	}
	// The metric only marks the beginning of a block, so what is still in force
	// is decided by the derived expiry, not by the series being present.
	live := make([]*blockedIPEpisode, 0, len(episodes))
	for _, episode := range episodes {
		if episode.FirstSeen+blockedIPBlockingTimeout > now.Unix() {
			live = append(live, episode)
		}
	}
	if err = blockedIPEmptyAfterNarrowing(window, len(live), false); err != nil {
		return
	}
	return a.resolve(live, start, now, window), window, nil
}

// ListHistory returns blockings that started inside [start, end]. Blocks still
// in force are included on purpose, so results overlap with ListCurrent: "which
// IPs were blocked recently" should not have a hole where the current ones are.
func (a *BlockedIPAdmin) ListHistory(filter *BlockedIPFilter, start, end time.Time) (blocked []*BlockedIP, window *BlockedIPWindow, err error) {
	// Anything older than the retention window has no data anyway.
	if earliest := end.Add(-blockedIPMaxWindow); start.Before(earliest) {
		start = earliest
	}
	window = &BlockedIPWindow{Requested: start, Start: start, End: end}
	episodes, err := a.queryEpisodes(filter, start, end, blockedIPStep(end.Sub(start)), window)
	if err != nil {
		return
	}
	if err = blockedIPEmptyAfterNarrowing(window, len(episodes), true); err != nil {
		return
	}
	return a.resolve(episodes, start, end, window), window, nil
}

// ListHostnames names every compute node the blocking metric has carried a
// label for, so the filter can be a dropdown instead of a field the operator has
// to spell correctly -- the filter matches the label exactly, so a single typo
// reads as "nothing is blocked" rather than as a mistake.
//
// The names come from the label index rather than from the hyper table because
// only the index is guaranteed to match what the filter compares against:
// export_blocked_ips.sh writes the label from `hostname -f` on the node itself,
// which need not be the hostname the database holds, and an option that can
// never match is worse than no option at all.
//
// The lookup spans the whole retention rather than the window on screen. It
// reads no samples, so the span costs nothing, and a node whose blockings are
// older than the current range is exactly the one an operator is about to widen
// the range to find.
func (a *BlockedIPAdmin) ListHostnames() (hostnames []string, err error) {
	end := time.Now().UTC()
	params := url.Values{}
	params.Set("match[]", blockedIPMetric)
	params.Set("start", strconv.FormatInt(end.Add(-blockedIPMaxWindow).Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))
	resp, err := blockedIPGet("label/hostname/values", params, blockedIPMetric)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	result := &blockedIPLabelValuesResponse{}
	if err = json.NewDecoder(resp.Body).Decode(result); err != nil {
		logger.Errorf("Failed to decode prometheus label values response, %v", err)
		return nil, err
	}
	if result.Status == "error" {
		err = fmt.Errorf("prometheus reported an error: %s: %s", result.ErrorType, result.Error)
		logger.Errorf("Prometheus reported an error: %v, label: hostname", err)
		return nil, err
	}
	return result.Data, nil
}

// blockedIPLabelValuesResponse is what /api/v1/label/<name>/values answers: the
// distinct values a label takes, without the series carrying them, so its size
// is set by the number of compute nodes and by nothing else.
type blockedIPLabelValuesResponse struct {
	Status    string   `json:"status"`
	ErrorType string   `json:"errorType"`
	Error     string   `json:"error"`
	Data      []string `json:"data"`
}

// BlockedIPBudgetError says the range asked for holds more than one query may
// move, and that narrowing it to what fits left nothing behind.
//
// Given a distinct type because the two callers must answer differently: an API
// client can act on the numbers, so it gets a 400 rather than a 500, and the web
// page must keep its form on screen -- telling an operator to narrow the range
// from an error page that has no date pickers on it is a dead end.
type BlockedIPBudgetError struct {
	Points    int64
	Budget    int64
	CanNarrow bool
}

func (e *BlockedIPBudgetError) Error() string {
	advice := "filter by ip, hostname or block_type"
	if e.CanNarrow {
		advice = "narrow the range to when the blockings happened, or " + advice
	}
	return fmt.Sprintf("the requested range holds %d data points, over the %d this query may move, and the most recent part of it that fits holds no blocking at all; %s",
		e.Points, e.Budget, advice)
}

// blockedIPEmptyAfterNarrowing turns the one bad narrowing outcome into a plain
// refusal.
//
// Landing on a slice that holds nothing is not a partial answer, it is no answer
// with a warning attached -- and it happens whenever the blockings sit further
// back than the slice the budget affords, which is exactly the shape of "a big
// attack yesterday, quiet today".
func blockedIPEmptyAfterNarrowing(window *BlockedIPWindow, episodes int, canNarrow bool) error {
	if window == nil || !window.Truncated || episodes > 0 || window.Points <= blockedIPMaxPoints {
		return nil
	}
	return &BlockedIPBudgetError{Points: window.Points, Budget: blockedIPMaxPoints, CanNarrow: canNarrow}
}

// blockedIPFit is what the probe concluded about one query: where to actually
// start, how much data the window as asked for holds, and how much the narrowed
// one holds. Requested is the number that matters to a caller -- it is what
// overflowed. Served says whether narrowing found anything at all.
type blockedIPFit struct {
	Start     time.Time
	Requested int64
	Served    int64
}

// blockedIPFitQuery narrows a query's own window until the data behind it fits
// what this process may hold, and says where it landed.
//
// The probe asks the index, not the samples: /api/v1/series says how many series
// a selector matches over a window, and a range query returns at most one point
// per series per step. Every query this file makes is a bare selector, so it can
// be handed over as match[] with no per-query special casing.
//
// Counting stored samples instead -- the first thing tried -- measures the wrong
// quantity: it overstates the returned points by step/scrape_interval, which is
// 120x for the ownership hops at their hourly step, and it made the guard reject
// windows a hundred times smaller than the process could actually hold.
//
// Narrowing moves start forward rather than dropping rows: what an operator
// wants is the most recent data, still correctly ordered and counted. The loss
// is always reported -- a caller handed a tenth of the answer with no hint is
// worse off than one handed an error.
func blockedIPFitQuery(query string, start, end time.Time, step int64) (*blockedIPFit, error) {
	fit := &blockedIPFit{Start: start}
	for attempt := 0; ; attempt++ {
		points, err := blockedIPProbePoints(query, fit.Start, end, step)
		if err != nil {
			return nil, err
		}
		fit.Served = points
		if attempt == 0 {
			// The count for the window as asked for. Kept because it is the only
			// figure that explains the narrowing; every later probe reports the
			// already-shrunk window and is within budget by construction.
			fit.Requested = points
		}
		if points <= blockedIPMaxPoints {
			return fit, nil
		}
		span := end.Sub(fit.Start)
		if span <= blockedIPMinFitWindow || attempt >= blockedIPFitAttempts-1 {
			// Out of attempts, or already at the floor: serve the floor. Served
			// then describes the window measured last rather than the floor.
			fit.Start = end.Add(-blockedIPMinFitWindow)
			return fit, nil
		}
		// Scale by how far over budget we are, with a margin so a denser recent
		// stretch does not immediately overshoot again.
		scaled := time.Duration(float64(span) * float64(blockedIPMaxPoints) / float64(points) * blockedIPFitMargin)
		if scaled < blockedIPMinFitWindow {
			scaled = blockedIPMinFitWindow
		}
		if scaled >= span {
			return fit, nil
		}
		fit.Start = end.Add(-scaled)
	}
}

// blockedIPProbePoints is the upper bound on what a range query would return:
// one point per matching series per step. A selector matching nothing gives an
// empty list rather than an error, which is zero points, not a failure.
func blockedIPProbePoints(selector string, start, end time.Time, step int64) (int64, error) {
	series, err := blockedIPSeries(selector, start, end)
	if err != nil {
		return 0, err
	}
	if len(series) == 0 {
		return 0, nil
	}
	span := int64(end.Sub(start).Seconds())
	if span < 1 {
		span = 1
	}
	if step < 1 {
		step = 1
	}
	return int64(len(series)) * (span/step + 1), nil
}

// blockedIPStep picks the sample grid for a window.
//
// A fixed 300s grid silently loses short windows: the metric only exists while
// the address sits in the 300s info set, so a window of a couple of minutes can
// have its single grid point land before the block ever appeared and come back
// empty while the current view still lists it. Cutting the window into at least
// blockedIPMinPoints points removes that hole. The floor is the exporter's own
// interval, below which there is nothing more to see; the ceiling keeps long
// windows at the same cost as before.
func blockedIPStep(window time.Duration) int64 {
	step := int64(window.Seconds()) / blockedIPMinPoints
	if step < blockedIPCurrentStep {
		return blockedIPCurrentStep
	}
	if step > blockedIPHistoryStep {
		return blockedIPHistoryStep
	}
	return step
}

// queryEpisodes runs the range query and groups every series into blockings.
// The metric is only present for the first few minutes of a block, so a gap
// wider than two steps means a separate blocking rather than a pause.
func (a *BlockedIPAdmin) queryEpisodes(filter *BlockedIPFilter, start, end time.Time, step int64, window *BlockedIPWindow) (episodes []*blockedIPEpisode, err error) {
	resp, err := blockedIPQueryRange(blockedIPMetric+blockedIPSelector(filter), start, end, step, window)
	if err != nil {
		return
	}
	gap := step * 2
	for _, series := range resp.Data.Result {
		ip := series.Metric["ip"]
		if ip == "" {
			continue
		}
		var current *blockedIPEpisode
		for _, sample := range series.Values {
			ts, okTs := blockedIPSampleTime(sample)
			if !okTs {
				continue
			}
			if current != nil && ts-current.LastSeen > gap {
				episodes = append(episodes, current)
				current = nil
			}
			if current == nil {
				current = &blockedIPEpisode{
					IP:        ip,
					Hostname:  series.Metric["hostname"],
					BlockType: series.Metric["block_type"],
					FirstSeen: ts,
				}
			}
			current.LastSeen = ts
		}
		if current != nil {
			episodes = append(episodes, current)
		}
	}
	// Newest first, then grouped by node: the two halves of one incident (the
	// attacker and its target) are blocked on the same node at the same moment,
	// so this ordering puts them on adjacent rows without having to guess which
	// rows belong together.
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].FirstSeen != episodes[j].FirstSeen {
			return episodes[i].FirstSeen > episodes[j].FirstSeen
		}
		if episodes[i].Hostname != episodes[j].Hostname {
			return episodes[i].Hostname < episodes[j].Hostname
		}
		return episodes[i].IP < episodes[j].IP
	})
	return
}

// resolve turns the raw episodes into the exported form, attaching ownership.
func (a *BlockedIPAdmin) resolve(episodes []*blockedIPEpisode, start, end time.Time, window *BlockedIPWindow) (blocked []*BlockedIP) {
	owners := a.resolveOwners(episodes, start, end)
	// Only consulted for rows that found no owner, and then only once per
	// five minute bucket: a row that named an instance has its answer already,
	// and blockings arrive in bursts that share a moment.
	healthAt := map[int64]bool{}
	healthyAt := func(at time.Time) bool {
		bucket := at.Truncate(blockedIPOwnerProbeWindow).Unix()
		if healthy, done := healthAt[bucket]; done {
			return healthy
		}
		healthy := blockedIPMapHealthyAt(at)
		healthAt[bucket] = healthy
		return healthy
	}
	blocked = make([]*BlockedIP, 0, len(episodes))
	for _, episode := range episodes {
		entry := &BlockedIP{
			IP:        episode.IP,
			BlockType: blockedIPType(episode.BlockType),
			Hostname:  episode.Hostname,
			Source:    "none",
		}
		if owner, found := owners[episode]; found {
			entry.InstanceUUID = owner.InstanceUUID
			entry.LbID = owner.LbID
			entry.Ours = owner.Ours()
			entry.Source = owner.Source
			entry.OwnerState = owner.State
		} else if !healthyAt(time.Unix(episode.FirstSeen, 0).UTC()) {
			// A miss only means "not ours" while the mapping was actually being
			// published at the time of this blocking. When it was not, saying so
			// is the whole answer: an unavailable mapping would otherwise turn
			// every address in the region into an external attacker, silently
			// and all at once.
			entry.OwnerState = blockedIPOwnerStateUnavailable
		}
		// Ownership decides the direction, and membership of the mapping settles
		// it on its own. Testing the UUID instead would conflate "we cannot name
		// the instance" with "it is not ours", which is exactly what a reserved
		// address, a detached floating IP and a load balancer VIP all look like:
		// ours, holding no VM. Direction is left unknown when the mapping could
		// not be consulted, since every branch of it would be a guess.
		entry.Direction = blockedIPDirection(episode.BlockType, entry.Ours)
		if entry.OwnerState == blockedIPOwnerStateUnavailable {
			entry.Direction = "unknown"
		}
		entry.BlockedAt = time.Unix(episode.FirstSeen, 0).UTC()
		entry.ExpiresAt = entry.BlockedAt.Add(blockedIPBlockingTimeout * time.Second)
		blocked = append(blocked, entry)
	}
	return
}

// Owner states beyond a clean resolution.
const (
	// The address maps to more than one instance, so the pair no longer
	// identifies anything and the instance shown must not be acted on.
	blockedIPOwnerStateConflict = "conflict"
	// The mapping was not being published across the window, so neither a hit
	// nor a miss means anything.
	blockedIPOwnerStateUnavailable = "unavailable"
)

// How far either side of a blocking to look when deciding which instance held
// an address at that moment. Wide enough to span a scrape interval so a probe
// cannot land between two samples and see nothing, narrow enough that a handover
// hours or days away falls outside it.
const blockedIPOwnerProbeWindow = 5 * time.Minute

// Ceiling on disambiguation probes per request. Ambiguity needs an address of
// ours that changed hands inside the window, which is rare -- an attack's
// thousands of foreign addresses are not in the mapping at all and never reach
// here. The cap exists so that anomalous data cannot turn one page render into
// thousands of queries; whatever it cuts off is reported as unresolved rather
// than guessed at.
const blockedIPOwnerProbeLimit = 50

// resolveOwners maps each blocking back to the instance that held the address
// when it happened.
//
// Keyed by episode rather than by address, because an address can change hands
// between two blockings of it and each one deserves the answer that was true at
// its own moment.
//
// One broad lookup answers everything unambiguous. Only an address whose broad
// window holds more than one distinct instance_id -- including the empty one,
// since "held no VM" is a state the address can move in and out of -- needs a
// second, narrow lookup around the blocking itself. Both read the label index
// and no samples, so neither is subject to the size guard and a long history
// window costs no more than a short one.
func (a *BlockedIPAdmin) resolveOwners(episodes []*blockedIPEpisode, start, end time.Time) map[*blockedIPEpisode]*BlockedIPOwner {
	owners := map[*blockedIPEpisode]*BlockedIPOwner{}
	ips := make([]string, 0, len(episodes))
	seen := map[string]bool{}
	for _, episode := range episodes {
		if !seen[episode.IP] {
			seen[episode.IP] = true
			ips = append(ips, episode.IP)
		}
	}
	if len(ips) == 0 {
		return owners
	}
	selector := blockedIPInstanceMapMetric + "{ip=~\"" + blockedIPAlternation(ips) + "\"}"
	series, err := blockedIPSeries(selector, start, end)
	if err != nil {
		// Deliberately not fatal. The blockings themselves are already known and
		// worth showing; what is lost is the name beside them, and the health
		// probe reports the loss so no row claims to be external on the strength
		// of a query that never ran.
		logger.Errorf("Failed to query ip instance map: %v", err)
		return owners
	}
	broad := map[string][]map[string]string{}
	for _, labels := range series {
		if ip := labels["ip"]; ip != "" {
			broad[ip] = append(broad[ip], labels)
		}
	}

	// A probe answers for every blocking of the same address that falls inside
	// the same window, so blockings in a burst share one query.
	probed := map[string]*BlockedIPOwner{}
	probes := 0
	for _, episode := range episodes {
		candidates, found := broad[episode.IP]
		if !found {
			continue
		}
		if owner := blockedIPOwnerOf(candidates); owner != nil {
			owners[episode] = owner
			continue
		}
		// Several distinct instances across the window. Which one held the
		// address when this blocking started is a question only a narrower
		// window can answer.
		at := time.Unix(episode.FirstSeen, 0).UTC()
		bucket := fmt.Sprintf("%s@%d", episode.IP, at.Truncate(blockedIPOwnerProbeWindow).Unix())
		if owner, done := probed[bucket]; done {
			owners[episode] = owner
			continue
		}
		if probes >= blockedIPOwnerProbeLimit {
			logger.Errorf("Reached the %d probe ceiling while disambiguating ip instance mappings; %s is reported unresolved rather than attributed by guess",
				blockedIPOwnerProbeLimit, episode.IP)
			owner := &BlockedIPOwner{Source: "metric", State: blockedIPOwnerStateConflict}
			probed[bucket] = owner
			owners[episode] = owner
			continue
		}
		probes++
		owner := a.probeOwner(episode.IP, at)
		probed[bucket] = owner
		owners[episode] = owner
	}
	return owners
}

// blockedIPOwnerOf answers from the broad window alone, or nil when it cannot.
//
// nil means the address carried more than one distinct instance_id across the
// window, which is either a handover or a genuine conflict; the two look
// identical here and only a narrower window tells them apart. A conflict the API
// itself flagged needs no such distinction -- it was already found at one
// instant -- so it is answered immediately.
func blockedIPOwnerOf(candidates []map[string]string) *BlockedIPOwner {
	distinct := map[string]bool{}
	instanceID := ""
	lbID := ""
	for _, labels := range candidates {
		if labels["status"] == blockedIPMapStatusConflict {
			return &BlockedIPOwner{InstanceUUID: labels["instance_id"], LbID: labels["lb_id"], Source: "metric", State: blockedIPOwnerStateConflict}
		}
		distinct[labels["instance_id"]] = true
		if labels["instance_id"] != "" {
			instanceID = labels["instance_id"]
		}
		if labels["lb_id"] != "" {
			lbID = labels["lb_id"]
		}
	}
	if len(distinct) > 1 {
		return nil
	}
	return &BlockedIPOwner{InstanceUUID: instanceID, LbID: lbID, Source: "metric"}
}

// probeOwner asks which instance held the address around one moment.
//
// An empty answer is not treated as "not ours": the broad window already
// established that the address is one of ours, and a gap in the mapping at this
// particular instant says only that the instance cannot be named. Letting it
// read as foreign would undo the whole point of consulting the mapping.
func (a *BlockedIPAdmin) probeOwner(ip string, at time.Time) *BlockedIPOwner {
	selector := blockedIPInstanceMapMetric + "{ip=\"" + blockedIPEscapeLabel(ip) + "\"}"
	series, err := blockedIPSeries(selector, at.Add(-blockedIPOwnerProbeWindow), at.Add(blockedIPOwnerProbeWindow))
	if err != nil {
		logger.Errorf("Failed to probe ip instance mapping for %s at %s: %v", ip, at.Format(time.RFC3339), err)
		return &BlockedIPOwner{Source: "metric", State: blockedIPOwnerStateConflict}
	}
	if owner := blockedIPOwnerOf(series); owner != nil {
		return owner
	}
	// Still more than one instance five minutes either side of the blocking.
	// That is no longer a handover -- they overlapped -- so the pair genuinely
	// does not identify anything here.
	logger.Errorf("Address %s maps to more than one instance around %s; reported as a conflict rather than attributed to one of them",
		ip, at.Format(time.RFC3339))
	return &BlockedIPOwner{Source: "metric", State: blockedIPOwnerStateConflict}
}

// blockedIPMapHealthyAt reports whether the mapping was being published at one
// particular moment.
//
// Asked per blocking rather than once per window, and as an instant query rather
// than an aggregate over the range. min_over_time was wrong for this: it takes
// the minimum of the samples that exist, and a gap produces no sample at all, so
// an outage is masked by whatever healthy samples share the window. Over the
// history tab's window that is up to 720 hours of masking -- a mapping that was
// down for the entire span an address existed would still be called healthy, and
// the address then reads as foreign.
//
// An empty result is the answer that matters most. Prometheus marks a target's
// series stale the moment a scrape fails, so an exporter that stopped, or one
// that was never deployed, produces nothing here rather than a zero -- and
// calling that healthy would label every address in the region an external
// attacker at once.
func blockedIPMapHealthyAt(at time.Time) bool {
	params := url.Values{}
	params.Set("query", blockedIPMapHealthMetric)
	params.Set("time", strconv.FormatInt(at.Unix(), 10))
	resp, err := blockedIPQuery("query", params, blockedIPMapHealthMetric)
	if err != nil {
		logger.Errorf("Failed to probe ip instance map health at %s: %v", at.Format(time.RFC3339), err)
		return false
	}
	if len(resp.Data.Result) == 0 {
		logger.Warningf("No %s sample at %s; an ownership miss at that moment cannot be read as an external address",
			blockedIPMapHealthMetric, at.Format(time.RFC3339))
		return false
	}
	// Several regions may report into one Prometheus, and a mapping that was
	// down anywhere cannot be relied on here.
	for _, series := range resp.Data.Result {
		if len(series.Value) < 2 {
			return false
		}
		text, ok := series.Value[1].(string)
		if !ok {
			return false
		}
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || value < 1 {
			return false
		}
	}
	return true
}

// blockedIPQueryRange runs a PromQL range query against the Prometheus already
// configured for the alarm subsystem (monitor.host / monitor.port).
// Every range query passes through here, so this is where the size guard lives:
// one budget, one place, applied to the blocking metric and to both ownership
// hops alike. window may be nil for a caller that does not report narrowing.
func blockedIPQueryRange(query string, start, end time.Time, step int64, window *BlockedIPWindow) (result *blockedIPPromResponse, err error) {
	fit, err := blockedIPFitQuery(query, start, end, step)
	if err != nil {
		return nil, err
	}
	if fit.Start.After(start) {
		logger.Warningf("Narrowed a blocked-ip query from %s to %s: %d points exceeds the %d budget, query: %s",
			start.Format(time.RFC3339), fit.Start.Format(time.RFC3339), fit.Requested, blockedIPMaxPoints, query)
		window.narrowedTo(fit.Start, fit.Requested)
	} else {
		window.narrowedTo(time.Time{}, fit.Requested)
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(fit.Start.Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))
	params.Set("step", strconv.FormatInt(step, 10))
	return blockedIPQuery("query_range", params, query)
}

// blockedIPSeriesResponse is what /api/v1/series answers: label sets and nothing
// else, so its size is set by cardinality alone and never by window length.
type blockedIPSeriesResponse struct {
	Status    string              `json:"status"`
	ErrorType string              `json:"errorType"`
	Error     string              `json:"error"`
	Data      []map[string]string `json:"data"`
}

// blockedIPSeries asks the index which series a selector matches over a window.
// It reads no samples, which serves two callers: sizing a range query before
// running it, and the domain -> instance_id hop, which only ever wanted labels.
func blockedIPSeries(selector string, start, end time.Time) ([]map[string]string, error) {
	params := url.Values{}
	params.Set("match[]", selector)
	params.Set("start", strconv.FormatInt(start.Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))
	resp, err := blockedIPGet("series", params, selector)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	result := &blockedIPSeriesResponse{}
	if err = json.NewDecoder(resp.Body).Decode(result); err != nil {
		logger.Errorf("Failed to decode prometheus series response, %v", err)
		return nil, err
	}
	if result.Status == "error" {
		err = fmt.Errorf("prometheus reported an error: %s: %s", result.ErrorType, result.Error)
		logger.Errorf("Prometheus reported an error: %v, selector: %s", err, selector)
		return nil, err
	}
	return result.Data, nil
}

func blockedIPQuery(path string, params url.Values, query string) (result *blockedIPPromResponse, err error) {
	resp, err := blockedIPGet(path, params, query)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	result = &blockedIPPromResponse{}
	if err = json.NewDecoder(resp.Body).Decode(result); err != nil {
		logger.Errorf("Failed to decode prometheus response, %v", err)
		return nil, err
	}
	// A 200 carrying status "error" is not documented behaviour, but treating it
	// as success would hand the caller an empty result set that looks like "no
	// blocks" rather than "the query never ran".
	if result.Status == "error" {
		err = fmt.Errorf("prometheus reported an error: %s", blockedIPFailureText(result))
		logger.Errorf("Prometheus reported an error: %v, query: %s", err, query)
		return nil, err
	}
	return
}

// blockedIPGet issues one Prometheus API call and hands back a response already
// known to carry a 200. Both the PromQL endpoints and the series endpoint go
// through it, so a rejection reads the same whichever one was asked.
func blockedIPGet(path string, params url.Values, query string) (*http.Response, error) {
	host := alarmPrometheusIP
	if host == "" {
		host = "localhost"
	}
	port := alarmPrometheusPort
	if port == 0 {
		port = 9090
	}
	endpoint := fmt.Sprintf("http://%s:%d/api/v1/%s?%s", host, port, path, params.Encode())

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		logger.Errorf("Prometheus query failed, %s, %v", query, err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// Prometheus answers a rejected query with a structured reason: the
		// sample limit tripped, the expression failed to parse, the evaluation
		// timed out. Dropping it made every one of those an indistinguishable
		// 500, so the reason travels with the error while the query itself --
		// which can run to thousands of characters once the address alternation
		// is built -- stays in the log.
		err = fmt.Errorf("prometheus rejected the query: %s", blockedIPPromFailure(resp))
		logger.Errorf("Prometheus query rejected: %v, query: %s", err, query)
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// blockedIPPromFailure renders a non-200 answer as something an operator can act
// on, falling back to the raw body when it is not the documented JSON shape.
func blockedIPPromFailure(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, blockedIPErrorBodyLimit))
	failure := &blockedIPPromResponse{}
	if json.Unmarshal(body, failure) == nil && failure.Error != "" {
		return fmt.Sprintf("HTTP %d, %s", resp.StatusCode, blockedIPFailureText(failure))
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return fmt.Sprintf("HTTP %d, %s", resp.StatusCode, text)
	}
	return fmt.Sprintf("HTTP %d with no reason given", resp.StatusCode)
}

func blockedIPFailureText(failure *blockedIPPromResponse) string {
	if failure.ErrorType != "" {
		return fmt.Sprintf("%s: %s", failure.ErrorType, failure.Error)
	}
	if failure.Error != "" {
		return failure.Error
	}
	return "no reason given"
}

func blockedIPType(blockType string) string {
	if blockType == "" {
		return "unknown"
	}
	return blockType
}

// blockedIPDirection states what the metrics support and no more: which side of
// the flood this address was on, and whether it is one of ours.
//
// check_halfopen_connections.sh puts the SYN source in block_src and the SYN
// destination in block_dst, so the side is always known. Ownership is a separate
// question from identity: whether the address is ours is answered by the first
// hop, while which instance holds it needs the second. An address with a domain
// but no UUID is still ours, and the caller passes ours accordingly.
//
//	src + ours     our VM is flooding outward
//	src + unknown  an address flooding us that is no running VM of ours
//	dst + ours     our VM being flooded
//	dst + unknown  an address being flooded that we cannot attribute
//
// The last line used to claim more -- "the foreign address our VM is flooding".
// Staging disproved it. On 2026-08-03 12:53:20, 104.233.207.163, .165 and .173
// were flooded in the same second by the same fourteen external sources; only
// .163 had a vm_ip series, so it read "our VM under attack" while the other two
// read "target of our attack", the opposite conclusion from identical evidence.
// black_list.log names every source in those incidents and not one is ours.
//
// The thresholds say the same thing. They are dst, src = dst*3/5 and
// src_dst = src/2 (report_rc.sh:186-196), so a VM of ours flooding hard enough
// to trip the dst threshold trips the lower src one first and appears as
// src + ours. A blocked dst with no such row beside it is a victim, not
// something we hit. Blocking a dst says the address was flooded and nothing
// about who flooded it, so neither does this function: better no direction than
// one pointing the wrong way, which sends an operator to isolate their own VM
// when they should be shielding it.
func blockedIPDirection(blockType string, ours bool) string {
	switch {
	case blockType != "src" && blockType != "dst":
		return "unknown"
	case blockType == "dst" && ours:
		return "vm_under_attack"
	case blockType == "dst":
		return "target_under_attack"
	case ours:
		return "vm_compromised"
	default:
		return "external_attacker"
	}
}

func blockedIPSelector(filter *BlockedIPFilter) string {
	if filter == nil {
		return ""
	}
	matchers := []string{}
	if filter.IP != "" {
		matchers = append(matchers, "ip=\""+blockedIPEscapeLabel(filter.IP)+"\"")
	}
	if filter.Hostname != "" {
		matchers = append(matchers, "hostname=\""+blockedIPEscapeLabel(filter.Hostname)+"\"")
	}
	if filter.BlockType != "" {
		matchers = append(matchers, "block_type=\""+blockedIPEscapeLabel(filter.BlockType)+"\"")
	}
	if len(matchers) == 0 {
		return ""
	}
	return "{" + strings.Join(matchers, ",") + "}"
}

func blockedIPSampleTime(sample []interface{}) (int64, bool) {
	if len(sample) < 2 {
		return 0, false
	}
	ts, ok := sample[0].(float64)
	if !ok {
		return 0, false
	}
	return int64(ts), true
}

var blockedIPLabelUnsafe = regexp.MustCompile(`[^0-9a-zA-Z.:_/-]`)

// blockedIPEscapeLabel drops anything that could break out of a label matcher.
// Every value matched on is an IP, a hostname or src/dst.
func blockedIPEscapeLabel(value string) string {
	return blockedIPLabelUnsafe.ReplaceAllString(value, "")
}

// blockedIPAlternation builds a regex alternation, escaping the dots in IPs so
// they cannot act as wildcards.
//
// The result is embedded in a double-quoted PromQL string, which processes
// escape sequences of its own before the regex ever sees them: a lone backslash
// from QuoteMeta makes PromQL reject the whole query as an unknown escape
// sequence, so each one is doubled to survive that pass.
func blockedIPAlternation(values []string) string {
	escaped := make([]string, 0, len(values))
	for _, value := range values {
		quoted := regexp.QuoteMeta(blockedIPEscapeLabel(value))
		escaped = append(escaped, strings.ReplaceAll(quoted, `\`, `\\`))
	}
	return strings.Join(escaped, "|")
}
