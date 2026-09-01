/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/model"
)

// Whether a mapping can be trusted. Kept to a closed, tiny set on purpose: a
// consumer turns this into a label, and anything free-form would make the
// cardinality of the metric depend on how much went wrong. The detail behind a
// conflict goes to the log, where it costs nothing.
const (
	IPMapStatusOK       = "ok"
	IPMapStatusConflict = "conflict"
)

// IPInstanceMapAdmin answers "which of our addresses is held by what".
//
// It exists so that ownership of an address can be settled without asking
// whether a VM happens to be running. The only metric carrying that association
// today is domain_north_south_*{vm_ip}, which a VM emits only while it runs, so
// a reserved address, a detached floating IP, a load balancer VIP and a powered
// off VM all resolve to nothing there -- and then read as somebody else's
// address, which is the opposite of the truth.
type IPInstanceMapAdmin struct{}

// IPInstanceMapping is one address of ours and what holds it.
//
// InstanceID empty is meaningful rather than missing: the address is ours, it
// simply holds no VM right now. That is exactly the case a reserved or detached
// address falls into, and the case the traffic metric can never express.
type IPInstanceMapping struct {
	IP         string
	InstanceID string
	// floating or classic, produced by the query that found the address rather
	// than decided here: nothing in Go compares it, so the value travels
	// untouched from SQL to the exporter's label. A constant on this side would
	// be a second place to keep the same decision, with nothing checking that
	// the two agree.
	Category string
	Type     string
	LbID     string
	Status   string
}

// List returns every address we own, floating and classic alike.
//
// Two queries and no per-row work. That is the whole design constraint: this is
// scraped on an interval, so its cost must not scale into the database. The
// floating IP list endpoint next door runs five to eight queries per row because
// it assembles what a page needs -- zone, interface, router, owner. A lookup
// table needs none of that.
func (a *IPInstanceMapAdmin) List(ctx context.Context) (mappings []*IPInstanceMapping, err error) {
	memberShip := GetMemberShip(ctx)
	if !memberShip.CheckPermission(model.Admin) {
		logger.Error("Not authorized for this operation")
		return nil, NewCLError(ErrPermissionDenied, "Not authorized for this operation", nil)
	}
	_, db := GetContextDB(ctx)

	// One statement rather than two cursors. Both halves answer the same
	// question from different tables, so joining them here keeps the scan loop
	// single and stops the first cursor being held open across the second query.
	//
	// ip_address is the bare address but is not filled in on every path --
	// createDummyFloatingIp sets only fip_address -- so the mask is stripped off
	// that as a fallback; native addresses are created exactly that way and
	// would otherwise be missing entirely.
	//
	// An address reaches an interface as either its primary or one of its
	// secondary addresses, and both are the VM's to answer for: an allowed
	// address pair is just as attackable as the address the NIC booted with.
	query := `
		SELECT COALESCE(NULLIF(f.ip_address, ''), split_part(f.fip_address, '/', 1)) AS ip,
		       COALESCE(i.uuid, '') AS instance_id,
		       'floating' AS category,
		       COALESCE(f.type, '') AS fip_type,
		       COALESCE(f.load_balancer_id, 0) AS lb_id
		FROM floating_ips f
		LEFT JOIN instances i ON i.id = f.instance_id AND i.deleted_at IS NULL
		WHERE f.deleted_at IS NULL
		UNION ALL
		SELECT split_part(a.address, '/', 1),
		       COALESCE(i.uuid, ''),
		       'classic',
		       '',
		       0
		FROM addresses a
		JOIN interfaces n ON n.deleted_at IS NULL AND n.id IN (a.interface, a.second_interface)
		JOIN instances i ON i.id = n.instance AND i.deleted_at IS NULL
		WHERE a.deleted_at IS NULL`
	rows, err := db.Raw(query).Rows()
	if err != nil {
		logger.Errorf("Failed to query ip instance mappings, %v", err)
		return nil, NewCLError(ErrSQLSyntaxError, "Failed to query ip instance mappings", err)
	}
	defer rows.Close()
	collected := []*IPInstanceMapping{}
	for rows.Next() {
		var ip, instanceID, category, fipType string
		var lbID int64
		if err = rows.Scan(&ip, &instanceID, &category, &fipType, &lbID); err != nil {
			logger.Errorf("Failed to scan ip instance mapping, %v", err)
			return nil, NewCLError(ErrSQLSyntaxError, "Failed to scan ip instance mapping", err)
		}
		if ip == "" {
			continue
		}
		mapping := &IPInstanceMapping{IP: ip, InstanceID: instanceID, Category: category, Type: fipType, Status: IPMapStatusOK}
		// Only load balancer addresses carry this, and they are precisely the
		// ones with no instance to name -- without it they would be
		// indistinguishable from an address attached to nothing at all.
		if lbID > 0 {
			mapping.LbID = strconv.FormatInt(lbID, 10)
		}
		collected = append(collected, mapping)
	}
	if err = rows.Err(); err != nil {
		logger.Errorf("Failed to read ip instance mappings, %v", err)
		return nil, NewCLError(ErrSQLSyntaxError, "Failed to read ip instance mappings", err)
	}
	return reconcileIPMappings(collected), nil
}

// reconcileIPMappings turns the rows the two queries returned into the mapping
// itself, one address at a time.
//
// Three things can bring an address back more than once, and only the last is a
// fault:
//
//   - the same pair twice, because the address reaches an interface through
//     both interface and second_interface. One pair, reported once.
//   - the same address with and without an instance. Not a disagreement: the
//     empty side says "ours, holding no VM", so the row naming an instance is
//     the complete answer.
//   - the same address against two different instances. The pair no longer
//     identifies anything, and there is no basis for preferring either one.
//
// The last case reports every candidate rather than picking one. Picking would
// present a coin flip as an answer and silently discard the evidence that it
// was a coin flip; reporting both leaves the conflict visible to whoever has to
// resolve it, and status marks each row so no consumer mistakes them for
// ordinary mappings.
func reconcileIPMappings(rows []*IPInstanceMapping) []*IPInstanceMapping {
	order := []string{}
	byIP := map[string][]*IPInstanceMapping{}
	for _, row := range rows {
		if _, found := byIP[row.IP]; !found {
			order = append(order, row.IP)
		}
		byIP[row.IP] = append(byIP[row.IP], row)
	}

	mappings := make([]*IPInstanceMapping, 0, len(order))
	for _, ip := range order {
		candidates := byIP[ip]
		// Keep one row per distinct instance, so a pair repeated by the join
		// collapses while genuinely different instances all survive.
		distinct := []*IPInstanceMapping{}
		seen := map[string]bool{}
		for _, candidate := range candidates {
			if seen[candidate.InstanceID] {
				continue
			}
			seen[candidate.InstanceID] = true
			distinct = append(distinct, candidate)
		}
		// An address holding no VM is only the answer when nothing else claims
		// it; any row naming an instance supersedes it.
		named := []*IPInstanceMapping{}
		for _, candidate := range distinct {
			if candidate.InstanceID != "" {
				named = append(named, candidate)
			}
		}
		if len(named) == 0 {
			mappings = append(mappings, distinct[0])
			continue
		}
		if len(named) == 1 {
			mappings = append(mappings, named[0])
			continue
		}
		instances := make([]string, 0, len(named))
		for _, candidate := range named {
			candidate.Status = IPMapStatusConflict
			instances = append(instances, candidate.InstanceID)
			mappings = append(mappings, candidate)
		}
		logger.Errorf("Address %s maps to %d instances (%s); the ip to instance pair is no longer unique, so every candidate is reported and none of them can be trusted",
			ip, len(instances), strings.Join(instances, ", "))
	}
	return mappings
}
