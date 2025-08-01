/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	secgroupAdmin = &SecgroupAdmin{}
	secgroupView  = &SecgroupView{}
)

type SecgroupAdmin struct{}
type SecgroupView struct{}

func (a *SecgroupAdmin) Switch(ctx context.Context, newSg *model.SecurityGroup, router *model.Router) (err error) {
	ctx, db := GetContextDB(ctx)
	oldSg := &model.SecurityGroup{}
	if router != nil {
		oldSg.ID = router.DefaultSG
		err = db.Take(oldSg).Error
		if err != nil {
			logger.Error("Failed to query default security group", err)
			return
		}
		router.DefaultSG = newSg.ID
		err = db.Model(router).Update("default_sg", router.DefaultSG).Error
		if err != nil {
			logger.Error("Failed to save router", err)
			return
		}
	} else {
		memberShip := GetMemberShip(ctx)
		var org *model.Organization
		org, err = orgAdmin.Get(ctx, memberShip.OrgID)
		if err != nil {
			logger.Error("Failed to query organization ", err)
			return
		}
		if org.DefaultSG > 0 {
			oldSg.ID = org.DefaultSG
			err = db.Take(oldSg).Error
			if err != nil {
				logger.Error("Failed to query default security group", err)
				return
			}
		}
		org.DefaultSG = newSg.ID
		err = db.Model(org).Update("default_sg", org.DefaultSG).Error
		if err != nil {
			logger.Error("DB failed to update org default sg", err)
			return
		}
	}
	if oldSg.ID > 0 {
		oldSg.IsDefault = false
		err = db.Model(oldSg).Update("is_default", oldSg.IsDefault).Error
		if err != nil {
			logger.Error("Failed to save new security group", err)
			return
		}
	}
	newSg.IsDefault = true
	err = db.Model(newSg).Update("is_default", newSg.IsDefault).Error
	if err != nil {
		logger.Error("Failed to save new security group", err)
		return
	}
	return
}

func (a *SecgroupAdmin) Update(ctx context.Context, secgroup *model.SecurityGroup, name string, isDefault bool) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	if name != "" && secgroup.Name != name {
		secgroup.Name = name
	}
	if isDefault && secgroup.IsDefault != isDefault {
		secgroup.IsDefault = isDefault
		if isDefault {
			a.Switch(ctx, secgroup, secgroup.Router)
		}
	}
	err = db.Model(secgroup).Updates(secgroup).Error
	if err != nil {
		logger.Error("Failed to save security group", err)
		return
	}
	return
}

func (a *SecgroupAdmin) Get(ctx context.Context, id int64) (secgroup *model.SecurityGroup, err error) {
	if id <= 0 {
		return a.GetSecgroupByName(ctx, SystemDefaultSGName)
	}
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	where := memberShip.GetWhere()
	secgroup = &model.SecurityGroup{Model: model.Model{ID: id}}
	err = db.Where(where).Take(secgroup).Error
	if err != nil {
		logger.Error("DB failed to query secgroup ", err)
		return
	}
	if secgroup.RouterID > 0 {
		secgroup.Router = &model.Router{Model: model.Model{ID: secgroup.RouterID}}
		err = db.Take(secgroup.Router).Error
		if err != nil {
			logger.Error("DB failed to qeury router", err)
			return
		}
	}
	if secgroup.Name != "system-default" {
		permit := memberShip.ValidateOwner(model.Reader, secgroup.Owner)
		if !permit {
			logger.Error("Not authorized to get security group")
			err = fmt.Errorf("Not authorized")
			return
		}
	}
	return
}

func (a *SecgroupAdmin) GetSecgroupByUUID(ctx context.Context, uuID string) (secgroup *model.SecurityGroup, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	secgroup = &model.SecurityGroup{}
	err = db.Where(where).Where("uuid = ?", uuID).Take(secgroup).Error
	if err != nil {
		logger.Error("Failed to query secgroup ", err)
		return
	}
	if secgroup.RouterID > 0 {
		secgroup.Router = &model.Router{Model: model.Model{ID: secgroup.RouterID}}
		err = db.Take(secgroup.Router).Error
		if err != nil {
			logger.Error("DB failed to qeury router", err)
			return
		}
	}
	if secgroup.Name != "system-default" {
		permit := memberShip.ValidateOwner(model.Reader, secgroup.Owner)
		if !permit {
			logger.Error("Not authorized to get security group")
			err = fmt.Errorf("Not authorized")
			return
		}
	}
	return
}

func (a *SecgroupAdmin) GetDefaultSecgroup(ctx context.Context) (secgroup *model.SecurityGroup, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	org, err := orgAdmin.Get(ctx, memberShip.OrgID)
	if err != nil {
		logger.Error("Failed to query organization ", err)
		return
	}
	if org.DefaultSG == 0 {
		timestamp := time.Now().UnixNano()
		secgroupName := fmt.Sprintf("default-%d", timestamp)
		secgroup, err = a.Create(ctx, secgroupName, true, nil)
		if err != nil {
			logger.Error("Failed to create account secgroup ", err)
			return
		}
		org.DefaultSG = secgroup.ID
		err = db.Model(org).Update("default_sg", org.DefaultSG).Error
		if err != nil {
			logger.Error("DB failed to update org default sg", err)
			return
		}
	} else {
		secgroup = &model.SecurityGroup{Model: model.Model{ID: org.DefaultSG}}
		err = db.Model(secgroup).Take(secgroup).Error
		if err != nil {
			logger.Error("Failed to query account secgroup ", err)
			return
		}
	}
	return
}

func (a *SecgroupAdmin) GetSecgroupByName(ctx context.Context, name string) (secgroup *model.SecurityGroup, err error) {
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	secgroup = &model.SecurityGroup{}
	err = db.Where("name = ?", name).Take(secgroup).Error
	if err != nil {
		logger.Error("Failed to query secgroup ", err)
		return
	}
	if secgroup.RouterID > 0 {
		secgroup.Router = &model.Router{Model: model.Model{ID: secgroup.RouterID}}
		err = db.Take(secgroup.Router).Error
		if err != nil {
			logger.Error("Failed to query router ", err)
			return
		}
	}
	if secgroup.Name != "system-default" {
		permit := memberShip.ValidateOwner(model.Reader, secgroup.Owner)
		if !permit {
			logger.Error("Not authorized to get security group")
			err = fmt.Errorf("Not authorized")
			return
		}
	}
	return
}

func (a *SecgroupAdmin) GetSecurityGroup(ctx context.Context, reference *BaseReference) (secgroup *model.SecurityGroup, err error) {
	if reference == nil || (reference.ID == "" && reference.Name == "") {
		err = fmt.Errorf("Security group base reference must be provided with either uuid or name")
		return
	}
	if reference.ID != "" {
		secgroup, err = a.GetSecgroupByUUID(ctx, reference.ID)
		return
	}
	if reference.Name != "" {
		secgroup, err = a.GetSecgroupByName(ctx, reference.Name)
		return
	}
	return
}

func (a *SecgroupAdmin) GetSecgroupInterfaces(ctx context.Context, secgroup *model.SecurityGroup) (err error) {
	ctx, db := GetContextDB(ctx)
	err = db.Model(secgroup).Preload("Address").Preload("Address.Subnet").Preload("SecondAddresses").Preload("SecondAddresses.Subnet").Preload("SiteSubnets").Where("instance > 0").Related(&secgroup.Interfaces, "Interfaces").Error
	if err != nil {
		logger.Error("Failed to query secgroup, %v", err)
		return
	}
	return
}

func (a *SecgroupAdmin) GetInterfaceSecgroups(ctx context.Context, iface *model.Interface) (err error) {
	ctx, db := GetContextDB(ctx)
	err = db.Model(iface).Related(&iface.SecurityGroups, "Security_Groups").Error
	if err != nil {
		logger.Error("Failed to query interface, %v", err)
		return
	}
	return
}

func (a *SecgroupAdmin) AllowPortForInterfaceSecgroups(ctx context.Context, port int32, iface *model.Interface) (err error) {
	if port > 0 && port != 22 && port != 3389 {
		for _, sg := range iface.SecurityGroups {
			_, err = secruleAdmin.Create(ctx, "0.0.0.0/0", "ingress", "tcp", port, port, sg)
			if err != nil {
				logger.Error("Failed to create security rule", err)
				return
			}
		}
	}
	return
}

func (a *SecgroupAdmin) RemovePortForInterfaceSecgroups(ctx context.Context, port int32, iface *model.Interface) (err error) {
	if port > 0 && port != 22 && port != 3389 {
		for _, sg := range iface.SecurityGroups {
			_, err = secruleAdmin.DeleteRule(ctx, "0.0.0.0/0", "ingress", "tcp", port, port, sg)
			if err != nil {
				logger.Error("Failed to remove security rule", err)
				return
			}
		}
	}
	return
}

func (a *SecgroupAdmin) Create(ctx context.Context, name string, isDefault bool, router *model.Router) (secgroup *model.SecurityGroup, err error) {
	memberShip := GetMemberShip(ctx)
	owner := memberShip.OrgID
	var routerID int64
	if router != nil {
		permit := memberShip.ValidateOwner(model.Writer, router.Owner)
		if !permit {
			logger.Error("Not authorized for this operation")
			err = fmt.Errorf("Not authorized")
			return
		}
		routerID = router.ID
	} else {
		permit := memberShip.CheckPermission(model.Owner)
		if !permit {
			logger.Error("Not authorized for this operation")
			err = fmt.Errorf("Not authorized")
			return
		}
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	secgroup = &model.SecurityGroup{Model: model.Model{Creater: memberShip.UserID}, Owner: owner, Name: name, IsDefault: isDefault, RouterID: routerID}
	err = db.Create(secgroup).Error
	if err != nil {
		logger.Errorf("DB failed to create security group %s, %v", name, err)
		return
	}
	_, err = secruleAdmin.Create(ctx, "0.0.0.0/0", "egress", "tcp", 1, 65535, secgroup)
	if err != nil {
		logger.Error("Failed to create security rule", err)
		return
	}
	_, err = secruleAdmin.Create(ctx, "0.0.0.0/0", "egress", "udp", 1, 65535, secgroup)
	if err != nil {
		logger.Error("Failed to create security rule", err)
		return
	}
	_, err = secruleAdmin.Create(ctx, "0.0.0.0/0", "ingress", "tcp", 22, 22, secgroup)
	if err != nil {
		logger.Error("Failed to create security rule", err)
		return
	}
	_, err = secruleAdmin.Create(ctx, "0.0.0.0/0", "ingress", "tcp", 3389, 3389, secgroup)
	if err != nil {
		logger.Error("Failed to create security rule", err)
		return
	}
	_, err = secruleAdmin.Create(ctx, "0.0.0.0/0", "ingress", "udp", 68, 68, secgroup)
	if err != nil {
		logger.Error("Failed to create security rule", err)
		return
	}
	_, err = secruleAdmin.Create(ctx, "0.0.0.0/0", "egress", "icmp", -1, -1, secgroup)
	if err != nil {
		logger.Error("Failed to create security rule", err)
		return
	}
	_, err = secruleAdmin.Create(ctx, "0.0.0.0/0", "ingress", "icmp", -1, -1, secgroup)
	if err != nil {
		logger.Error("Failed to create security rule", err)
		return
	}
	if router != nil {
		var subnets []*model.Subnet
		err = db.Where("router_id = ?", router.ID).Find(&subnets).Error
		if err != nil {
			logger.Error("Failed to create security rule", err)
			return
		}
		for _, subnet := range subnets {
			_, err = secruleAdmin.Create(ctx, subnet.Network, "ingress", "tcp", 1, 65535, secgroup)
			if err != nil {
				logger.Error("Failed to create security rule", err)
				return
			}
			_, err = secruleAdmin.Create(ctx, subnet.Network, "ingress", "udp", 1, 65535, secgroup)
			if err != nil {
				logger.Error("Failed to create security rule", err)
				return
			}
		}
	}
	if isDefault {
		err = a.Switch(ctx, secgroup, router)
		if err != nil {
			logger.Error("Failed to set default security group", err)
			return
		}
	}
	return
}

func (a *SecgroupAdmin) Delete(ctx context.Context, secgroup *model.SecurityGroup) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, secgroup.Owner)
	if !permit {
		logger.Error("Not authorized to delete the router")
		err = fmt.Errorf("Not authorized")
		return
	}
	if secgroup.IsDefault == true && secgroup.Name != SystemDefaultSGName {
		if secgroup.RouterID > 0 {
			router := &model.Router{Model: model.Model{ID: secgroup.RouterID}}
			err = db.Where("default_sg = ?", secgroup.ID).Take(&router).Error
			if err == nil {
				logger.Error("Default security group can not be deleted", err)
				err = fmt.Errorf("Default security group can not be deleted")
				return
			}
		} else {
			_, err = orgAdmin.Get(ctx, secgroup.Owner)
			if err == nil {
				logger.Error("Default security group can not be deleted", err)
				err = fmt.Errorf("Default security group can not be deleted")
				return
			}
		}
	}
	err = db.Model(secgroup).Related(&secgroup.Interfaces, "Interfaces").Error
	if err != nil {
		logger.Error("Failed to count the number of interfaces using the security group", err)
		return
	}
	if len(secgroup.Interfaces) > 0 {
		logger.Error("Security group can not be deleted if there are associated interfaces")
		err = fmt.Errorf("The security group can not be deleted if there are associated interfaces")
		return
	}
	err = db.Where("secgroup = ?", secgroup.ID).Delete(&model.SecurityRule{}).Error
	if err != nil {
		logger.Error("DB failed to delete security group rules", err)
		return
	}
	secgroup.Name = fmt.Sprintf("%s-%d", secgroup.Name, secgroup.CreatedAt.Unix())
	err = db.Model(secgroup).Update("name", secgroup.Name).Error
	if err != nil {
		logger.Error("DB failed to update security group name", err)
		return
	}
	if err = db.Delete(secgroup).Error; err != nil {
		logger.Error("DB failed to delete security group", err)
		return
	}
	return
}

func (a *SecgroupAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, secgroups []*model.SecurityGroup, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		err = fmt.Errorf("Not authorized")
		return
	}
	ctx, db := GetContextDB(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}
	logger.Debugf("The query in admin console is %s", query)

	where := memberShip.GetWhere()
	secgroups = []*model.SecurityGroup{}
	if err = db.Model(&model.SecurityGroup{}).Where(where).Where(query).Count(&total).Error; err != nil {
		logger.Error("DB failed to count security group(s), %v", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where(where).Where(query).Find(&secgroups).Error; err != nil {
		logger.Error("DB failed to query security group(s), %v", err)
		return
	}
	for _, secgroup := range secgroups {
		if secgroup.RouterID > 0 {
			secgroup.Router = &model.Router{Model: model.Model{ID: secgroup.RouterID}}
			err = db.Take(secgroup.Router).Error
			if err != nil {
				logger.Error("DB failed to qeury router", err)
				err = nil
				continue
			}
		}
	}
	permit = memberShip.CheckPermission(model.Admin)
	if permit {
		db = db.Offset(0).Limit(-1)
		for _, sg := range secgroups {
			sg.OwnerInfo = &model.Organization{Model: model.Model{ID: sg.Owner}}
			if err = db.Take(sg.OwnerInfo).Error; err != nil {
				logger.Error("Failed to query owner info", err)
				err = nil
				continue
			}
		}
	}

	return
}

func (v *SecgroupView) List(c *macaron.Context, store session.Store) {
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	router_id := c.QueryTrim("router_id")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}
	if router_id != "" {
		routerID, err := strconv.Atoi(router_id)
		if err != nil {
			logger.Debugf("Error to convert router_id to integer: %+v ", err)
		}
		query = fmt.Sprintf("router_id = %d", routerID)
	}

	total, secgroups, err := secgroupAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list security group(s), %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["SecurityGroups"] = secgroups
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "secgroups")
}

func (v *SecgroupView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.Error(http.StatusBadRequest)
		return
	}
	secgroupID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid security group ID, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	secgroup, err := secgroupAdmin.Get(ctx, int64(secgroupID))
	if err != nil {
		logger.Error("Failed to get security group", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = secgroupAdmin.Delete(ctx, secgroup)
	if err != nil {
		logger.Errorf("Failed to delete security group, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "secgroups",
	})
	return
}
func (v *SecgroupView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Writer)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	routers := []*model.Router{}
	err := DB().Find(&routers).Error
	if err != nil {
		logger.Error("Database failed to query gateways", err)
		return
	}
	c.Data["Routers"] = routers
	c.HTML(200, "secgroups_new")
}

func (v *SecgroupView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	db := DB()
	id := c.Params(":id")
	sgID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	permit, err := memberShip.CheckOwner(model.Writer, "security_groups", int64(sgID))
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	secgroup := &model.SecurityGroup{Model: model.Model{ID: int64(sgID)}}
	err = db.Take(secgroup).Error
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, err.Error())
		return
	}
	c.Data["Secgroup"] = secgroup
	c.HTML(200, "secgroups_patch")
}

func (v *SecgroupView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	memberShip := GetMemberShip(c.Req.Context())
	redirectTo := "../secgroups"
	id := c.Params(":id")
	name := c.QueryTrim("name")
	sgID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	permit, err := memberShip.CheckOwner(model.Writer, "security_groups", int64(sgID))
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	isdefStr := c.QueryTrim("isdefault")
	isDef := false
	if isdefStr == "" || isdefStr == "no" {
		isDef = false
	} else if isdefStr == "yes" {
		isDef = true
	}
	secgroup, err := secgroupAdmin.Get(ctx, int64(sgID))
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	err = secgroupAdmin.Update(ctx, secgroup, name, isDef)
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	c.Redirect(redirectTo)
	return
}

func (v *SecgroupView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "../secgroups"
	name := c.QueryTrim("name")
	isdefStr := c.QueryTrim("isdefault")
	isDef := false
	if isdefStr == "" || isdefStr == "no" {
		isDef = false
	} else if isdefStr == "yes" {
		isDef = true
	}
	var router *model.Router
	var err error
	routerID := c.QueryInt64("router")
	if routerID > 0 {
		router, err = routerAdmin.Get(ctx, routerID)
		if err != nil {
			logger.Error("Failed to get vpc", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(404, "404")
		}
	}
	_, err = secgroupAdmin.Create(ctx, name, isDef, router)
	if err != nil {
		logger.Error("Failed to create security group, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	c.Redirect(redirectTo)
}
