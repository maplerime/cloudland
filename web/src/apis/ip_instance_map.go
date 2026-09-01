/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package apis

import (
	"net/http"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var ipInstanceMapAPI = &IPInstanceMapAPI{}
var ipInstanceMapAdminAPI = &routes.IPInstanceMapAdmin{}

type IPInstanceMapAPI struct{}

// IPInstanceMappingResponse is one address of ours and what holds it.
type IPInstanceMappingResponse struct {
	// The address itself, without any mask.
	IP string `json:"ip" example:"107.148.235.43"`
	// UUID of the instance holding the address. Empty is meaningful rather than
	// missing: the address is ours but holds no VM, which is the normal state of
	// a reserved address, a detached floating IP and a load balancer VIP.
	InstanceID string `json:"instance_id" example:"0376dec9-1891-492f-aa00-1e67afe23a7a"`
	// Which kind of address this is. One of: floating, classic.
	Category string `json:"category" example:"floating"`
	// Subtype, for floating addresses only; empty for classic. One of: native,
	// reserved, floating, site, loadbalancer.
	Type string `json:"type" example:"floating"`
	// Load balancer holding the address, non-empty only for type loadbalancer.
	// Those rows have no instance_id, so without this there would be nothing at
	// all naming what holds them.
	//
	// Always present, empty when it does not apply. Every row carries the same
	// six keys on purpose: a consumer maps them straight onto a fixed label set
	// without having to test which fields exist.
	LbID string `json:"lb_id" example:""`
	// Whether this row's mapping can be trusted. One of:
	//
	//   ok        the address maps to exactly one instance, or to none at all
	//   conflict  the address was found mapping to more than one instance
	//
	// A conflict row still names an instance, but that instance is one of
	// several candidates and must not be acted on. The address itself is still
	// known to belong to this region, which is why the row is reported rather
	// than dropped. The instances involved are named in the server log.
	//
	// Deliberately a closed set of short values: consumers turn it into a label,
	// and a free-form message there would make the metric's cardinality depend
	// on how much had gone wrong.
	Status string `json:"status" example:"ok"`
}

// IPInstanceMapListResponse is the whole mapping.
type IPInstanceMapListResponse struct {
	// Number of mappings returned.
	Total int `json:"total"`
	// Every address we own. Not paged: this is a lookup table meant to be
	// consumed whole, and paging it would only invite a torn read across pages.
	Entries []*IPInstanceMappingResponse `json:"entries"`
}

// List returns the address to instance mapping for the whole region.
//
// @Summary      List IP to instance mappings
// @Description  Returns every address the region owns -- floating and classic alike --
// @Description  together with the instance holding it. Built for monitoring exporters
// @Description  that need to attribute an arbitrary address back to an instance.
// @Description
// @Description  An empty instance_id means the address is ours but holds no VM right
// @Description  now, which is the normal state of a reserved address, a detached
// @Description  floating IP and a load balancer VIP. That is the case traffic metrics
// @Description  cannot express, since a VM only emits them while it runs.
// @Description
// @Description  The result is not paged: it is a lookup table meant to be consumed
// @Description  whole. Two queries back it and no work is done per row, so the cost
// @Description  does not grow with how often it is scraped. Admin only.
// @tags Network
// @Accept       json
// @Produce      json
// @Success      200 {object} IPInstanceMapListResponse
// @Failure      403 {object} common.APIError "Not authorized"
// @Failure      500 {object} common.APIError "Failed to query the mapping"
// @Router       /metrics/ip-instance-map [get]
func (a *IPInstanceMapAPI) List(c *gin.Context) {
	memberShip := GetMemberShip(c.Request.Context())
	if !memberShip.CheckPermission(model.Admin) {
		ErrorResponse(c, http.StatusForbidden, "Not authorized for this operation", nil)
		return
	}
	mappings, err := ipInstanceMapAdminAPI.List(c.Request.Context())
	if err != nil {
		logger.Errorf("Failed to query ip instance mappings: %v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Failed to query ip instance mappings", err)
		return
	}
	entries := make([]*IPInstanceMappingResponse, 0, len(mappings))
	for _, mapping := range mappings {
		entries = append(entries, &IPInstanceMappingResponse{
			IP:         mapping.IP,
			InstanceID: mapping.InstanceID,
			Category:   mapping.Category,
			Type:       mapping.Type,
			LbID:       mapping.LbID,
			Status:     mapping.Status,
		})
	}
	c.JSON(http.StatusOK, &IPInstanceMapListResponse{Total: len(entries), Entries: entries})
}
