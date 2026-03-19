package routes

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
)

// BaseSearchParams holds common search filters shared by all resources.
// The Name field is used differently per resource (see nameColumn in apply helpers).
type BaseSearchParams struct {
	ID       int64    // exact match by numeric ID
	UUID     string   // exact match by UUID
	Name     string   // fuzzy match (column varies: "hostname" for Instance, "name" for others)
	Statuses []string // filter by status (IN)
	OwnerIDs []int64  // filter by owner org IDs (IN)
}

// InstanceSearchParams extends BaseSearchParams with instance-specific filters.
type InstanceSearchParams struct {
	BaseSearchParams
	RouterID int64    // filter by VPC router ID (exact)
	ImageIDs []int64  // filter by image ID (IN)
	IP       string   // fuzzy match on interface/address IP
	HyperIDs []int32  // filter by hypervisor host ID (IN)
	ZoneIDs  []int64  // filter by zone ID (IN)
}

// VolumeSearchParams extends BaseSearchParams with volume-specific filters.
type VolumeSearchParams struct {
	BaseSearchParams
	VolumeType  string   // "all", "data", "boot"
	InstanceIDs []int64  // filter by attached instance ID (IN)
	PoolIDs     []string // filter by storage pool ID (IN)
}

// ImageSearchParams extends BaseSearchParams with image-specific filters.
type ImageSearchParams struct {
	BaseSearchParams
	OSCodes    []string // filter by OS type: linux, windows, other (IN)
	OSFamilies []string // filter by OS family (IN)
}

// FloatingIpSearchParams extends BaseSearchParams with floating IP-specific filters.
type FloatingIpSearchParams struct {
	BaseSearchParams
	FipAddress     string // fuzzy match on floating IP address
	IntAddress     string // fuzzy match on internal IP address
	AnyQuery       string // OR search across fip_address, int_address, name (backward compat)
	InstanceID     int64  // exact match on instance_id (use -1 for "instance_id = 0")
	LoadBalancerID int64  // exact match on load_balancer_id
	Type           string // exact match on type (e.g. "public_floating", "public_site")
	// RawCondition is an escape hatch for complex conditions that can't be expressed
	// via typed fields. MUST only be set by trusted internal code, never from user input.
	RawCondition string
}

// --- Apply helpers (one per resource, avoids duplicating filter logic) ---

// applyBaseSearch applies BaseSearchParams filters to a GORM query.
// nameColumn specifies the DB column for name search (e.g. "hostname" for Instance, "name" for others).
func applyBaseSearch(db *gorm.DB, p *BaseSearchParams, nameColumn string) *gorm.DB {
	if p == nil {
		return db
	}
	if p.ID > 0 {
		db = db.Where("id = ?", p.ID)
	}
	if p.UUID != "" {
		db = db.Where("uuid = ?", p.UUID)
	}
	if p.Name != "" && nameColumn != "" {
		db = db.Where(nameColumn+" LIKE ?", "%"+p.Name+"%")
	}
	if len(p.Statuses) > 0 {
		db = db.Where("status IN (?)", p.Statuses)
	}
	if len(p.OwnerIDs) > 0 {
		db = db.Where("owner IN (?)", p.OwnerIDs)
	}
	return db
}

// ApplyInstanceSearch applies all InstanceSearchParams filters.
func ApplyInstanceSearch(db *gorm.DB, params *InstanceSearchParams) *gorm.DB {
	if params == nil {
		return db
	}
	db = applyBaseSearch(db, &params.BaseSearchParams, "hostname")
	if params.RouterID > 0 {
		db = db.Where("router_id = ?", params.RouterID)
	}
	if len(params.ImageIDs) > 0 {
		db = db.Where("image_id IN (?)", params.ImageIDs)
	}
	if len(params.HyperIDs) > 0 {
		db = db.Where("hyper IN (?)", params.HyperIDs)
	}
	if len(params.ZoneIDs) > 0 {
		db = db.Where("zone_id IN (?)", params.ZoneIDs)
	}
	// IP search requires JOINs — handled separately in InstanceAdmin.List
	// because it needs GROUP BY to avoid duplicate rows.
	return db
}

// ApplyVolumeSearch applies all VolumeSearchParams filters.
func ApplyVolumeSearch(db *gorm.DB, params *VolumeSearchParams) *gorm.DB {
	if params == nil {
		return db
	}
	db = applyBaseSearch(db, &params.BaseSearchParams, "name")
	if params.VolumeType == "data" {
		db = db.Where("booting = ?", false)
	} else if params.VolumeType == "boot" {
		db = db.Where("booting = ?", true)
	}
	if len(params.InstanceIDs) > 0 {
		db = db.Where("instance_id IN (?)", params.InstanceIDs)
	}
	if len(params.PoolIDs) > 0 {
		db = db.Where("pool_id IN (?)", params.PoolIDs)
	}
	return db
}

// ApplyImageSearch applies all ImageSearchParams filters.
func ApplyImageSearch(db *gorm.DB, params *ImageSearchParams) *gorm.DB {
	if params == nil {
		return db
	}
	db = applyBaseSearch(db, &params.BaseSearchParams, "name")
	if len(params.OSCodes) > 0 {
		db = db.Where("os_code IN (?)", params.OSCodes)
	}
	if len(params.OSFamilies) > 0 {
		db = db.Where("os_family IN (?)", params.OSFamilies)
	}
	return db
}

// ApplyFloatingIpSearch applies all FloatingIpSearchParams filters.
func ApplyFloatingIpSearch(db *gorm.DB, params *FloatingIpSearchParams) *gorm.DB {
	if params == nil {
		return db
	}
	if params.AnyQuery != "" {
		db = applyBaseSearch(db, &BaseSearchParams{
			ID:       params.ID,
			UUID:     params.UUID,
			Statuses: params.Statuses,
			OwnerIDs: params.OwnerIDs,
		}, "")
		db = db.Where(
			"fip_address LIKE ? OR int_address LIKE ? OR name LIKE ?",
			"%"+params.AnyQuery+"%", "%"+params.AnyQuery+"%", "%"+params.AnyQuery+"%",
		)
	} else {
		db = applyBaseSearch(db, &params.BaseSearchParams, "name")
		if params.FipAddress != "" {
			db = db.Where("fip_address LIKE ?", "%"+params.FipAddress+"%")
		}
		if params.IntAddress != "" {
			db = db.Where("int_address LIKE ?", "%"+params.IntAddress+"%")
		}
	}
	if params.InstanceID > 0 {
		db = db.Where("instance_id = ?", params.InstanceID)
	}
	if params.InstanceID == -1 {
		db = db.Where("instance_id = 0")
	}
	if params.LoadBalancerID > 0 {
		db = db.Where("load_balancer_id = ?", params.LoadBalancerID)
	}
	if params.Type != "" {
		db = db.Where("type = ?", params.Type)
	}
	if params.RawCondition != "" {
		db = db.Where(params.RawCondition)
	}
	return db
}

// --- Query parameter parsing helpers ---

// ParseInt64Slice parses a comma-separated string of int64 values.
func ParseInt64Slice(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if v, err := strconv.ParseInt(p, 10, 64); err == nil {
			result = append(result, v)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ParseInt32Slice parses a comma-separated string of int32 values.
func ParseInt32Slice(s string) []int32 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]int32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if v, err := strconv.ParseInt(p, 10, 32); err == nil {
			result = append(result, int32(v))
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ParseStringSlice parses a comma-separated string into a string slice.
func ParseStringSlice(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ParseBaseSearchParams extracts common search params from Gin query string.
func ParseBaseSearchParams(c *gin.Context) BaseSearchParams {
	p := BaseSearchParams{}
	if idStr := c.Query("id"); idStr != "" {
		if v, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			p.ID = v
		}
	}
	p.UUID = strings.TrimSpace(c.Query("uuid"))
	p.Name = strings.TrimSpace(c.DefaultQuery("name", c.DefaultQuery("query", "")))
	p.Statuses = ParseStringSlice(c.Query("status"))
	p.OwnerIDs = ParseInt64Slice(c.Query("owner"))
	return p
}
