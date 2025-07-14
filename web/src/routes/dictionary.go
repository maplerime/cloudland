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
	logger.Debugf("Enter DictionaryAdmin.Create, category=%s, name=%s, value=%s", category, name, value)
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Errorf("Not authorized for this operation")
		err = fmt.Errorf("Not authorized")
		return
	}
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
		logger.Debugf("Exit DictionaryAdmin.Create, dictionary=%+v, err=%v", dictionary, err)
	}()
	if value == "" {
		logger.Errorf("Value cannot be empty")
		err = fmt.Errorf("value cannot be empty")
		return
	}
	dictionary = &model.Dictionary{
		Category: category,
		Name:     name,
		Value:    value,
	}
	err = db.Create(dictionary).Error
	return
}

func (a *DictionaryAdmin) Get(ctx context.Context, id int64) (*model.Dictionary, error) {
	logger.Debugf("Enter DictionaryAdmin.Get, id=%d", id)
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	dictionary := &model.Dictionary{Model: model.Model{ID: id}}
	if err := db.Where(where).First(dictionary, id).Error; err != nil {
		logger.Debugf("DictionaryAdmin.Get: failed to get dictionary, id=%d, err=%v", id, err)
		logger.Debugf("Exit DictionaryAdmin.Get with error")
		return nil, fmt.Errorf("failed to get dictionary: %w", err)
	}
	logger.Debugf("DictionaryAdmin.Get: success, uuid=%s, dictionary=%+v", dictionary.UUID, dictionary)
	return dictionary, nil
}

func (a *DictionaryAdmin) List(ctx context.Context, offset, limit int64, order string, query string) (total int64, dictionaries []*model.Dictionary, err error) {
	logger.Debugf("Enter DictionaryAdmin.List, offset=%d, limit=%d, order=%s, query=%s", offset, limit, order, query)
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	if limit == 0 {
		limit = 16
	}

	if order == "" {
		order = "created_at"
	}

	dictionaries = []*model.Dictionary{}
	if err = db.Model(&model.Dictionary{}).Where(where).Where(query).Count(&total).Error; err != nil {
		logger.Debugf("DictionaryAdmin.List: count error, err=%v", err)
		logger.Debugf("Exit DictionaryAdmin.List with error")
		return
	}
	db = dbs.Sortby(db.Offset(offset).Limit(limit), order)
	if err = db.Where(where).Where(query).Find(&dictionaries).Error; err != nil {
		logger.Errorf("DictionaryAdmin.List: find error, err=%v", err)
		return
	}
	logger.Debugf("DictionaryAdmin.List: success, total=%d, count=%d", total, len(dictionaries))
	return
}
func (a *DictionaryAdmin) GetDictionaryByUUID(ctx context.Context, uuID string) (dictionaries *model.Dictionary, err error) {
	logger.Debugf("Enter DictionaryAdmin.GetDictionaryByUUID, uuID=%s", uuID)
	ctx, db := GetContextDB(ctx)
	memberShip := GetMemberShip(ctx)
	where := memberShip.GetWhere()
	dictionaries = &model.Dictionary{}
	err = db.Where(where).Where("uuid = ?", uuID).Take(dictionaries).Error
	if err != nil {
		logger.Errorf("DictionaryAdmin.GetDictionaryByUUID: failed, uuID=%s, err=%v", uuID, err)
		return
	}
	logger.Debugf("DictionaryAdmin.GetDictionaryByUUID: success, dictionary=%+v", dictionaries)
	return
}

func (a *DictionaryAdmin) Update(ctx context.Context, dictionaries *model.Dictionary, category string, name string, value string) (dictionary *model.Dictionary, err error) {
	logger.Debugf("Enter DictionaryAdmin.Update, id=%d, category=%s, name=%s, value=%s", dictionaries.ID, category, name, value)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
		logger.Debugf("Exit DictionaryAdmin.Update, dictionary=%+v, err=%v", dictionary, err)
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
		logger.Errorf("DictionaryAdmin.Update: save error, err=%v", err)
		return nil, err
	}
	dictionary = dictionaries
	logger.Debugf("DictionaryAdmin.Update: success, uuid=%s, dictionary=%+v", dictionary.UUID, dictionary)
	return
}

func (a *DictionaryAdmin) Find(ctx context.Context, category, value string) (dictionary *model.Dictionary, err error) {
	db := DB()
	dictionary = &model.Dictionary{}
	err = db.Where("category = ? AND value = ?", category, value).Take(dictionary).Error
	if err != nil {
		logger.Error("DictionaryAdmin.Find: failed to get dictionary", err)
		return
	}
	return
}

func (a *DictionaryAdmin) Delete(ctx context.Context, dictionaries *model.Dictionary) (err error) {
	logger.Debugf("Enter DictionaryAdmin.Delete, id=%d", dictionaries.ID)
	ctx, db, newTransaction := StartTransaction(ctx)
	defer func() {
		if newTransaction {
			EndTransaction(ctx, err)
		}
		logger.Debugf("Exit DictionaryAdmin.Delete, id=%d, err=%v", dictionaries.ID, err)
	}()
	memberShip := GetMemberShip(ctx)
	permit := memberShip.CheckPermission(model.Admin)
	if !permit {
		logger.Error("Not authorized to delete the dictinary")
		err = fmt.Errorf("Not authorized")
		return
	}
	dictionaries.Value = fmt.Sprintf("%s-%d", dictionaries.Value, dictionaries.CreatedAt.Unix())
	err = db.Model(dictionaries).Update("value", dictionaries.Value).Error
	if err != nil {
		logger.Error("DB failed to update dictionary value", err)
		return
	}
	if err = db.Delete(dictionaries).Error; err != nil {
		logger.Errorf("DictionaryAdmin.Delete: db delete error, err=%v", err)
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
