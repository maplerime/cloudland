/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

History:
   Date     Who ID    Description
   -------- --- ---   -----------
   01/13/19 nanjj  Initial code

*/

package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var (
	orgView  = &OrgView{}
	orgAdmin = &OrgAdmin{}
)

type OrgAdmin struct {
}

type OrgView struct{}

func (a *OrgAdmin) Create(ctx context.Context, name, owner, uuid string) (org *model.Organization, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if name != "admin" && !permit {
		logger.Error("Not authorized to delete the user")
		err = fmt.Errorf("Not authorized")
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	user := &model.User{Username: owner}
	err = db.Where(user).Take(user).Error
	if err != nil {
		logger.Error("Failed to query user", err)
		return
	}
	org = &model.Organization{
		Model: model.Model{Creater: memberShip.UserID},
		Owner: user.ID,
		Name:  name,
	}
	if uuid != "" {
		org.UUID = uuid
		logger.Infof("Create org with uuid: %s", uuid)
	}
	err = db.Create(org).Error
	if err != nil {
		logger.Error("DB failed to create organization ", err)
		return
	}
	member := &model.Member{UserID: user.ID, UserName: owner, OrgID: org.ID, OrgName: name, Role: model.Owner}
	err = db.Create(member).Error
	if err != nil {
		logger.Error("DB failed to create organization member ", err)
		return
	}
	user.Owner = org.ID
	err = db.Model(user).Updates(user).Error
	if err != nil {
		logger.Error("DB failed to update user owner", err)
		return
	}
	return
}

func (a *OrgAdmin) Update(ctx context.Context, orgID int64, members, users []string, roles []model.Role) (org *model.Organization, err error) {
	memberShip := GetMemberShip(ctx)
	ctx, db := GetContextDB(ctx)
	org = &model.Organization{Model: model.Model{ID: orgID}}
	err = db.Set("gorm:auto_preload", true).Take(org).Take(org).Error
	if err != nil {
		logger.Error("Failed to query organization", err)
		return
	}
	for _, name := range members {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		found := false
		for _, em := range org.Members {
			if name == em.UserName {
				found = true
				break
			}
		}
		if found == true {
			continue
		}
		user := &model.User{Username: name}
		err = db.Model(user).Where(user).Take(user).Error
		if err != nil || user.ID <= 0 {
			logger.Error("Failed to query user", err)
			err = nil
			continue
		}
		member := &model.Member{
			Model:    model.Model{Creater: memberShip.UserID},
			Owner:    orgID,
			UserName: name,
			UserID:   user.ID,
			OrgName:  org.Name,
			OrgID:    orgID,
			Role:     model.Reader,
		}
		err = db.Create(member).Error
		if err != nil {
			logger.Error("Failed to create member", err)
			err = nil
			continue
		}
	}
	for _, em := range org.Members {
		found := false
		for _, name := range members {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if name == em.UserName {
				found = true
				break
			}
		}
		if found == true {
			continue
		}
		member := &model.Member{
			UserName: em.UserName,
			OrgID:    orgID,
		}
		err = db.Where(member).Delete(member).Error
		if err != nil {
			logger.Error("Failed to delete member", err)
			err = nil
			continue
		}
	}
	for i, user := range users {
		err = db.Model(&model.Member{}).Where("user_name = ? and org_id = ?", user, orgID).Update("role", roles[i]).Error
		if err != nil {
			logger.Error("Failed to update member", err)
			err = nil
			continue
		}
	}
	return
}

func (a *OrgAdmin) Get(ctx context.Context, id int64) (org *model.Organization, err error) {
	if id <= 0 {
		err = fmt.Errorf("Invalid org ID: %d", id)
		logger.Error("%v", err)
		return
	}
	ctx, db := GetContextDB(ctx)
	org = &model.Organization{Model: model.Model{ID: id}}
	err = db.Take(org).Error
	if err != nil {
		logger.Error("Failed to query user, %v", err)
		return
	}
	return
}

func (a *OrgAdmin) GetOrgByUUID(ctx context.Context, uuID string) (org *model.Organization, err error) {
	ctx, db := GetContextDB(ctx)
	org = &model.Organization{}
	err = db.Where("uuid = ?", uuID).Take(org).Error
	if err != nil {
		logger.Error("Failed to query org, %v", err)
		return
	}
	return
}

func (a *OrgAdmin) GetOrgByName(ctx context.Context, name string) (org *model.Organization, err error) {
	org = &model.Organization{}
	ctx, db := GetContextDB(ctx)
	err = db.Take(org, &model.Organization{Name: name}).Error
	return
}

func (a *OrgAdmin) GetOrgName(ctx context.Context, id int64) (name string) {
	org := &model.Organization{Model: model.Model{ID: id}}
	ctx, db := GetContextDB(ctx)
	err := db.Take(org, &model.Organization{Name: name}).Error
	if err != nil {
		logger.Error("DB failed to query org", err)
		return
	}
	name = org.Name
	return
}

func (a *OrgAdmin) Delete(ctx context.Context, org *model.Organization) (err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to delete the user")
		err = fmt.Errorf("Not authorized")
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()

	count := 0
	err = db.Model(&model.Interface{}).Where("owner = ?", org.ID).Count(&count).Error
	if err != nil {
		logger.Error("DB failed to query interfaces, %v", err)
		return
	}
	if count > 0 {
		logger.Error("There are resources in this org", err)
		err = fmt.Errorf("There are resources in this org")
		return
	}
	err = db.Delete(&model.Member{}, `org_id = ?`, org.ID).Error
	if err != nil {
		logger.Error("DB failed to delete member, %v", err)
		return
	}
	keys := []*model.Key{}
	err = db.Where("owner = ?", org.ID).Find(&keys).Error
	if err != nil {
		logger.Error("DB failed to query keys", err)
		return
	}
	for _, key := range keys {
		err = keyAdmin.Delete(ctx, key)
		if err != nil {
			logger.Error("Can not delete key", err)
			return
		}
	}
	org.Name = fmt.Sprintf("%s-%d", org.Name, org.CreatedAt.Unix())
	err = db.Model(org).Update("name", org.Name).Error
	if err != nil {
		logger.Error("DB failed to update org name", err)
		return
	}
	err = db.Delete(org).Error
	if err != nil {
		logger.Error("DB failed to delete organization, %v", err)
		return
	}
	secgroups := []*model.SecurityGroup{}
	err = db.Where("owner = ?", org.ID).Find(&secgroups).Error
	if err != nil {
		logger.Error("DB failed to security groups", err)
		return
	}
	for _, secgroup := range secgroups {
		err = secgroupAdmin.Delete(ctx, secgroup)
		if err != nil {
			logger.Error("Can not delete security group", err)
			return
		}
	}
	return
}

func (a *OrgAdmin) List(ctx context.Context, offset, limit int64, order, query string) (total int64, orgs []*model.Organization, err error) {
	memberShip := GetMemberShip(ctx)
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	if query != "" {
		query = fmt.Sprintf("name like '%%%s%%'", query)
	}
	ctx, db := GetContextDB(ctx)
	user := &model.User{Model: model.Model{ID: memberShip.UserID}}
	err = db.Take(user).Error
	if err != nil {
		logger.Error("DB failed to query user, %v", err)
		return
	}
	where := ""
	if memberShip.OrgName != "admin" || memberShip.Role != model.Admin {
		where = fmt.Sprintf("id = %d", memberShip.OrgID)
	}
	orgs = []*model.Organization{}
	if err = db.Model(&orgs).Where(where).Where(query).Count(&total).Error; err != nil {
		logger.Error("DB failed to count organizations, %v", err)
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	err = db.Where(where).Where(query).Find(&orgs).Error
	if err != nil {
		logger.Error("DB failed to query organizations, %v", err)
		return
	}

	return
}

func (v *OrgView) List(c *macaron.Context, store session.Store) {
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	query := c.QueryTrim("q")
	total, orgs, err := orgAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list organizations, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(500, "500")
		return
	}
	pages := GetPages(total, limit)
	c.Data["Organizations"] = orgs
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "orgs")
}

func (v *OrgView) Delete(c *macaron.Context, store session.Store) (err error) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		logger.Error("ID is empty, %v", err)
		c.Data["ErrorMsg"] = "ID is empty"
		c.Error(http.StatusBadRequest)
		return
	}
	orgID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Invalid organization ID, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	org, err := orgAdmin.Get(ctx, int64(orgID))
	if err != nil {
		logger.Error("Failed to get org ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = orgAdmin.Delete(ctx, org)
	if err != nil {
		logger.Error("Failed to delete organization, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "orgs",
	})
	return
}

func (v *OrgView) New(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.HTML(200, "orgs_new")
}

func (v *OrgView) Edit(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	db := DB()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	orgID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if memberShip.Role != model.Admin && (memberShip.Role < model.Owner || memberShip.OrgID != int64(orgID)) {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	org := &model.Organization{Model: model.Model{ID: int64(orgID)}}
	if err = db.Preload("Members").Take(org).Error; err != nil {
		logger.Error("Organization query failed", err)
		return
	}
	org.OwnerUser = &model.User{Model: model.Model{ID: org.Owner}}
	if err = db.Take(org.OwnerUser).Error; err != nil {
		logger.Error("Owner user query failed", err)
		return
	}
	c.Data["Org"] = org
	c.HTML(200, "orgs_patch")
}

func (v *OrgView) Patch(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	orgID, err := strconv.Atoi(id)
	if err != nil {
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	if memberShip.Role != model.Admin && (memberShip.Role < model.Owner || memberShip.OrgID != int64(orgID)) {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../orgs/" + id
	members := c.QueryTrim("members")
	memberList := strings.Split(members, " ")
	userList := c.QueryStrings("names")
	roles := c.QueryStrings("roles")
	var roleList []model.Role
	for _, r := range roles {
		role, err := strconv.Atoi(r)
		if err != nil {
			logger.Error("Failed to convert role", err)
			c.Data["ErrorMsg"] = err.Error()
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		if memberShip.Role < model.Role(role) {
			logger.Error("Not authorized for this operation")
			c.Data["ErrorMsg"] = "Not authorized for this operation"
			c.HTML(http.StatusBadRequest, "error")
			return
		}
		roleList = append(roleList, model.Role(role))
	}
	_, err = orgAdmin.Update(c.Req.Context(), int64(orgID), memberList, userList, roleList)
	if err != nil {
		logger.Error("Failed to update organization, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}

func (v *OrgView) Create(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../orgs"
	name := c.QueryTrim("orgname")
	owner := c.QueryTrim("owner")
	_, err := orgAdmin.Create(c.Req.Context(), name, owner, "")
	if err != nil {
		logger.Error("Failed to create organization, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Redirect(redirectTo)
}
