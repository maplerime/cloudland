package apis

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	. "web/src/common"
	"web/src/model"
	"web/src/routes"

	"github.com/gin-gonic/gin"
)

var dictionaryAdmin = &routes.DictionaryAdmin{}
var dictionaryAPI = &DictionaryAPI{}

type DictionaryResponse struct {
	*ResourceReference
	Category string `json:"category"`
	Value    string `json:"value"`
	SubType1 string `json:"subtype1"`
	SubType2 string `json:"subtype2"`
	SubType3 string `json:"subtype3"`
}
type DictionaryListResponse struct {
	Offset       int                   `json:"offset"`
	Total        int                   `json:"total"`
	Limit        int                   `json:"limit"`
	Dictionaries []*DictionaryResponse `json:"dictionaries"`
}
type DictionaryPayload struct {
	Name     string `json:"name" binding:"required,min=2,max=32"`
	Category string `json:"category" binding:"omitempty"`
	Value    string `json:"value" binding:"required"`
	SubType1 string `json:"subtype1" binding:"omitempty"` // data center
	SubType2 string `json:"subtype2" binding:"omitempty"` // ddos/ ddospro / siteip
	SubType3 string `json:"subtype3" binding:"omitempty"` //
}

type DictionaryAPI struct{}

// @Summary list dictionaries
// @Description list dictionaries
// @tags Compute
// @Accept  json
// @Produce json
// @Success 200 {object} DictionaryListResponse
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /dictionaries [get]
func (v *DictionaryAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	offset, err := strconv.Atoi(offsetStr)
	queryStr := c.DefaultQuery("query", "")
	valueStr := c.DefaultQuery("value", "")
	category := c.DefaultQuery("category", "")
	subtype1 := c.DefaultQuery("subtype1", "")
	subtype2 := c.DefaultQuery("subtype2", "")
	subtype3 := c.DefaultQuery("subtype3", "")
	logger.Debugf("DictionaryAPI.List: offset=%s, limit=%s, query=%s, value=%s, category=%s, subtype1=%s, subtype2=%s, subtype3=%s", offsetStr, limitStr, queryStr, valueStr, category, subtype1, subtype2, subtype3)
	if err != nil {
		logger.Errorf("DictionaryAPI.List: invalid offset, offsetStr=%s, err=%v", offsetStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Errorf("DictionaryAPI.List: invalid limit, limitStr=%s, err=%v", limitStr, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		logger.Errorf("DictionaryAPI.List: invalid offset or limit, offset=%d, limit=%d", offset, limit)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}
	if queryStr != "" {
		logger.Debugf("DictionaryAPI.List: filter by name like %%s%%", queryStr)
		queryStr = fmt.Sprintf("name like '%%%s%%'", queryStr)
	}
	if valueStr != "" {
		logger.Debugf("DictionaryAPI.List: filter by value = %s", valueStr)
		queryStr = fmt.Sprintf("value = '%s'", valueStr)
	}
	if category != "" {
		logger.Debugf("DictionaryAPI.List: filter by category = %s", category)
		if queryStr != "" {
			queryStr = fmt.Sprintf("%s AND category = '%s'", queryStr, category)
		} else {
			queryStr = fmt.Sprintf("category = '%s'", category)
		}
	}
	if subtype1 != "" {
		logger.Debugf("DictionaryAPI.List: filter by subtype1 = %s", subtype1)
		if queryStr != "" {
			queryStr = fmt.Sprintf("%s AND subtype1 = '%s'", queryStr, subtype1)
		} else {
			queryStr = fmt.Sprintf("subtype1 = '%s'", subtype1)
		}
	}
	if subtype2 != "" {
		logger.Debugf("DictionaryAPI.List: filter by subtype2 = %s", subtype2)
		if queryStr != "" {
			queryStr = fmt.Sprintf("%s AND subtype2 = '%s'", queryStr, subtype2)
		} else {
			queryStr = fmt.Sprintf("subtype2 = '%s'", subtype2)
		}
	}
	if subtype3 != "" {
		logger.Debugf("DictionaryAPI.List: filter by subtype3 = %s", subtype3)
		if queryStr != "" {
			queryStr = fmt.Sprintf("%s AND subtype3 = '%s'", queryStr, subtype3)
		} else {
			queryStr = fmt.Sprintf("subtype3 = '%s'", subtype3)
		}
	}
	logger.Debugf("DictionaryAPI.List: final query string: %s", queryStr)
	total, dictionaries, err := dictionaryAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr)
	if err != nil {
		logger.Errorf("DictionaryAPI.List: list error, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to list dictionaries", err)
		return
	}
	dictionaryListResp := &DictionaryListResponse{
		Total:  int(total),
		Offset: offset,
		Limit:  len(dictionaries),
	}
	dictionaryList := make([]*DictionaryResponse, dictionaryListResp.Limit)
	for i, dictionary := range dictionaries {
		dictionaryList[i], err = v.getDictionaryResponse(ctx, dictionary)
		if err != nil {
			logger.Errorf("DictionaryAPI.List: getDictionaryResponse error, err=%v", err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	dictionaryListResp.Dictionaries = dictionaryList
	logger.Debugf("DictionaryAPI.List: success, resp=%+v", dictionaryListResp)
	c.JSON(http.StatusOK, dictionaryListResp)
	return
}

// @Summary create a dictionary
// @Description create a dictionary
// @tags Compute
// @Accept  json
// @Produce json
// @Param   message	body   DictionaryPayload  true   "Dictionary create payload"
// @Success 200 {array} DictionaryResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /dictionaries [post]
func (v *DictionaryAPI) Create(c *gin.Context) {
	logger.Debugf("Enter DictionaryAPI.Create")
	ctx := c.Request.Context()
	payload := &DictionaryPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("DictionaryAPI.Create: bind json error, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	var dictionary *model.Dictionary
	dictionary, err = dictionaryAdmin.Create(ctx, payload.Category, payload.Name, payload.Value, payload.SubType1, payload.SubType2, payload.SubType3)
	if err != nil {
		logger.Errorf("DictionaryAPI.Create: create error, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to create dictionary", err)
		return
	}
	dictionaryResp, err := v.getDictionaryResponse(ctx, dictionary)
	if err != nil {
		logger.Errorf("DictionaryAPI.Create: getDictionaryResponse error, err=%v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("DictionaryAPI.Create: success, resp=%+v", dictionaryResp)
	c.JSON(http.StatusOK, dictionaryResp)
}

func (v *DictionaryAPI) getDictionaryResponse(ctx context.Context, dictionary *model.Dictionary) (dictionaryResp *DictionaryResponse, err error) {
	owner := orgAdmin.GetOrgName(ctx, dictionary.Owner)
	dictionaryResp = &DictionaryResponse{
		ResourceReference: &ResourceReference{
			ID:        dictionary.UUID,
			Name:      dictionary.Name,
			Owner:     owner,
			CreatedAt: dictionary.CreatedAt.Format(TimeStringForMat),
			UpdatedAt: dictionary.UpdatedAt.Format(TimeStringForMat),
		},
		Category: dictionary.Category,
		Value:    dictionary.Value,
		SubType1: dictionary.SubType1,
		SubType2: dictionary.SubType2,
		SubType3: dictionary.SubType3,
	}
	return
}

// @Summary get a dictionary
// @Description get a dictionary
// @tags Compute
// @Accept  json
// @Produce json
// @Param   id  path  string  true  "Dictionary UUID"
// @Success 200 {object} DictionaryResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /dictionaries/{id} [get]
func (v *DictionaryAPI) Get(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Enter DictionaryAPI.Get uuID=%s", uuID)
	dictinary, err := dictionaryAdmin.GetDictionaryByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("DictionaryAPI.Get: invalid dictinary query, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid dictinary query", err)
		return
	}
	dictinaryResp, err := v.getDictionaryResponse(ctx, dictinary)
	if err != nil {
		logger.Errorf("DictionaryAPI.Get: getDictionaryResponse error, err=%v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("DictionaryAPI.Get: success, uuID=%s, resp=%+v", uuID, dictinaryResp)
	c.JSON(http.StatusOK, dictinaryResp)
}

// @Summary delete a dictionary
// @Description delete a dictionary
// @tags Compute
// @Accept  json
// @Produce json
// @Param   id  path  int  true  "Dictionary ID"
// @Success 200
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /dictionaries/{id} [delete]
func (v *DictionaryAPI) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("DictionaryAPI.Delete: delete dictionary uuID=%s", uuID)
	dictinary, err := dictionaryAdmin.GetDictionaryByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("DictionaryAPI.Delete: getDictionaryByUUID error, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = dictionaryAdmin.Delete(ctx, dictinary)
	if err != nil {
		logger.Errorf("DictionaryAPI.Delete: delete error, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
	logger.Debugf("DictionaryAPI.Delete: success, uuID=%s", uuID)
	c.JSON(http.StatusNoContent, nil)
}

// @Summary patch a dictionary
// @Description patch a dictionary
// @tags Compute
// @Accept  json
// @Produce json
// @Param   message	body   DictionaryPayload  true   "Dictionary create payload"
// @Success 200 {object} DictionaryResponse
// @Failure 400 {object} common.APIError "Bad request"
// @Failure 401 {object} common.APIError "Not authorized"
// @Router /dictionaries/{id} [patch]
func (v *DictionaryAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")
	logger.Debugf("Enter DictionaryAPI.Patch uuID=%s", uuID)
	payload := &DictionaryPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		logger.Errorf("DictionaryAPI.Patch: bind json error, err=%v", err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	dictionaries, err := dictionaryAdmin.GetDictionaryByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("DictionaryAPI.Patch: invalid dictionary query, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid dictionary query", err)
		return
	}
	var dictionary *model.Dictionary
	dictionary, err = dictionaryAdmin.Update(ctx, dictionaries, payload.Category, payload.Name, payload.Value, payload.SubType1, payload.SubType2, payload.SubType3)
	if err != nil {
		logger.Errorf("DictionaryAPI.Patch: update error, uuID=%s, err=%v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Failed to update dictionary", err)
		return
	}
	dictionaryResp, err := v.getDictionaryResponse(ctx, dictionary)
	if err != nil {
		logger.Errorf("DictionaryAPI.Patch: getDictionaryResponse error, err=%v", err)
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	logger.Debugf("DictionaryAPI.Patch: success, uuID=%s, resp=%+v", uuID, dictionaryResp)
	c.JSON(http.StatusOK, dictionaryResp)
}
