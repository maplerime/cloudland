package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	. "web/src/common"
	"web/src/dbs"
	"web/src/model"

	"github.com/go-macaron/session"
	macaron "gopkg.in/macaron.v1"
)

var dictionaryAdmin = &DictionaryAdmin{}

type DictionaryAdmin struct{}

func (a *DictionaryAdmin) Create(ctx context.Context, category string, name string, value string) (dictionary *model.Dictionary, err error) {
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized for this operation")
		err = fmt.Errorf("Not authorized")
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	dictionary = &model.Dictionary{
		Category: category,
		Name:     name,
		Value:    value,
	}
	err = db.Create(dictionary).Error
	return
}

func (a *DictionaryAdmin) Get(ctx context.Context, id int64) (*model.Dictionary, error) {
	db := dbs.DB()
	dictionary := &model.Dictionary{}
	if err := db.First(dictionary, id).Error; err != nil {
		return nil, fmt.Errorf("failed to get dictionary: %w", err)
	}
	return dictionary, nil
}

func (a *DictionaryAdmin) List(ctx context.Context, offset, limit int64, order string, query string) (total int64, dictionaries []*model.Dictionary, err error) {
	db := DB()
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	dictionaries = []*model.Dictionary{}
	if err = db.Model(&model.Dictionary{}).Where(query).Count(&total).Error; err != nil {
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where(query).Find(&dictionaries).Error; err != nil {
		return
	}

	return
}
func (a *DictionaryAdmin) GetDictionaryByUUID(ctx context.Context, uuID string) (dictionaries *model.Dictionary, err error) {
	db := DB()
	dictionaries = &model.Dictionary{}
	err = db.Where("uuid = ?", uuID).Take(dictionaries).Error
	if err != nil {
		logger.Error("Failed to query dictionaries, %v", err)
		return
	}
	return
}

func (a *DictionaryAdmin) Update(ctx context.Context, dictionaries *model.Dictionary, category string, name string, value string) (dictionary *model.Dictionary, err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to update the dictionary")
		err = fmt.Errorf("Not authorized")
		return
	}
	if category != "" && dictionaries.Category != category {
		dictionaries.Category = category
	}
	if name != "" && dictionaries.Name != name {
		dictionaries.Name = name
	}
	if value != "" && dictionaries.Value != value {
		dictionaries.Value = value
	}
	err = db.Model(dictionaries).Updates(dictionaries).Error
	if err != nil {
		logger.Error("Failed to save dictionary", err)
		return nil, err
	}
	return dictionaries, nil
}

func (a *DictionaryAdmin) Delete(ctx context.Context, dictionaries *model.Dictionary) (err error) {
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.ValidateOwner(model.Writer, dictionaries.Owner)
	if !permit {
		logger.Error("Not authorized to delete the dictinary")
		err = fmt.Errorf("Not authorized")
		return
	}
	if err = db.Delete(dictionaries).Error; err != nil {
		logger.Error("DB failed to delete dictionary", err)
		return
	}
	return
}

type DictionaryView struct{}

func (v *DictionaryView) List(c *macaron.Context, store session.Store) {
	memberShip := GetMemberShip(c.Req.Context())
	permit := memberShip.CheckPermission(model.Reader)
	if !permit {
		logger.Error("Not authorized for this operation")
		c.Data["ErrorMsg"] = "Not authorized for this operation"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	offset := c.QueryInt64("offset")
	limit := c.QueryInt64("limit")
	if limit == 0 {
		limit = 16
	}
	order := c.QueryTrim("order")
	if order == "" {
		order = "-created_at"
	}
	query := c.QueryTrim("q")
	total, dictionaries, err := dictionaryAdmin.List(c.Req.Context(), offset, limit, order, query)
	if err != nil {
		logger.Error("Failed to list dictionaries", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(500)
		return
	}
	pages := GetPages(total, limit)
	c.Data["Dictionaries"] = dictionaries
	c.Data["Total"] = total
	c.Data["Pages"] = pages
	c.Data["Query"] = query
	c.HTML(200, "dictionaries")
}

func (v *DictionaryView) New(c *macaron.Context, store session.Store) {
	c.HTML(200, "dictionaries_new")
}

func (v *DictionaryView) Create(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	redirectTo := "/dictionaries"
	category := c.QueryTrim("category")
	name := c.QueryTrim("name")
	value := c.QueryTrim("value")

	var err error
	_, err = dictionaryAdmin.Create(ctx, category, name, value)
	if err != nil {
		logger.Error("Failed to create dictionary, %v", err)
		c.HTML(500, "500")
		return
	}
	c.Redirect(redirectTo)
}

func (v *DictionaryView) Edit(c *macaron.Context, store session.Store) {
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	dictionaryID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	db := DB()
	dictionary := &model.Dictionary{Model: model.Model{ID: int64(dictionaryID)}}
	err = db.Set("gorm:auto_preload", true).Take(dictionary).Error
	if err != nil {
		logger.Error("Failed to query dictionary", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	c.Data["Dictionary"] = dictionary
	c.HTML(200, "dictionaries_patch")
}

func (v *DictionaryView) Patch(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "Id is Empty"
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	dictionaryID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get input id, %v", err)
		c.Data["ErrorMsg"] = err.Error()
		c.HTML(http.StatusBadRequest, "error")
		return
	}
	redirectTo := "../dictionaries"
	category := c.QueryTrim("category")
	name := c.QueryTrim("name")
	value := c.QueryTrim("value")
	dictionaries, err := dictionaryAdmin.Get(ctx, int64(dictionaryID))
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	_, err = dictionaryAdmin.Update(ctx, dictionaries, category, name, value)
	if err != nil {
		c.HTML(500, err.Error())
		return
	}
	c.Redirect(redirectTo)
	return
}

func (v *DictionaryView) Delete(c *macaron.Context, store session.Store) {
	ctx := c.Req.Context()
	id := c.Params("id")
	if id == "" {
		c.Data["ErrorMsg"] = "id does not exist"
		c.Error(http.StatusBadRequest)
		return
	}
	dictionaryID, err := strconv.Atoi(id)
	if err != nil {
		logger.Error("Failed to get dictionary id ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	dictionaries, err := dictionaryAdmin.Get(ctx, int64(dictionaryID))
	if err != nil {
		logger.Error("Failed to get dictionary ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	err = dictionaryAdmin.Delete(ctx, dictionaries)
	if err != nil {
		logger.Error("Failed to delete dictionary ", err)
		c.Data["ErrorMsg"] = err.Error()
		c.Error(http.StatusBadRequest)
		return
	}
	c.JSON(200, map[string]interface{}{
		"redirect": "/dictionaries",
	})
	return
}
