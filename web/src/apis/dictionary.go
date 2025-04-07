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
	logger.Debugf("List dictionaries with offset %s, limit %s, query %s, value %s", offsetStr, limitStr, queryStr, valueStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset: "+offsetStr, err)
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query limit: "+limitStr, err)
		return
	}
	if offset < 0 || limit < 0 {
		ErrorResponse(c, http.StatusBadRequest, "Invalid query offset or limit", err)
		return
	}
	if queryStr != "" {
		queryStr = fmt.Sprintf("name like '%%%s%%'", queryStr)
	}
	if valueStr != "" {
		queryStr = fmt.Sprintf("value = '%s'", valueStr)
	}
	total, dictionaries, err := dictionaryAdmin.List(ctx, int64(offset), int64(limit), "-created_at", queryStr)
	if err != nil {
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
			logger.Errorf("Failed to list dictionary response, %+v", err)
			ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
			return
		}
	}
	dictionaryListResp.Dictionaries = dictionaryList
	logger.Debugf("List dictionaries success, %+v", dictionaryListResp)
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
	ctx := c.Request.Context()
	payload := &DictionaryPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	var dictionary *model.Dictionary
	dictionary, err = dictionaryAdmin.Create(ctx, payload.Category, payload.Name, payload.Value)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to create dictionary", err)
		return
	}
	dictionaryResp, err := v.getDictionaryResponse(ctx, dictionary)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, dictionaryResp)
}
func (v *DictionaryAPI) getDictionaryResponse(ctx context.Context, dictionary *model.Dictionary) (dictionaryResp *DictionaryResponse, err error) {
	owner := orgAdmin.GetOrgName(dictionary.Owner)
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
	dictinary, err := dictionaryAdmin.GetDictionaryByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid dictinary query", err)
		return
	}
	dictinaryResp, err := v.getDictionaryResponse(ctx, dictinary)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
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
	logger.Debugf("Delete dictionary %s", uuID)
	dictinary, err := dictionaryAdmin.GetDictionaryByUUID(ctx, uuID)
	if err != nil {
		logger.Errorf("Failed to get dictinary %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Invalid query", err)
		return
	}
	err = dictionaryAdmin.Delete(ctx, dictinary)
	if err != nil {
		logger.Errorf("Failed to delete dictinary %s, %+v", uuID, err)
		ErrorResponse(c, http.StatusBadRequest, "Not able to delete", err)
		return
	}
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

	payload := &DictionaryPayload{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	dictionaries, err := dictionaryAdmin.GetDictionaryByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid dictionary query", err)
		return
	}
	var dictionary *model.Dictionary

	dictionary, err = dictionaryAdmin.Update(ctx, dictionaries, payload.Category, payload.Name, payload.Value)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to update dictionary", err)
		return
	}
	dictionaryResp, err := v.getDictionaryResponse(ctx, dictionary)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, "Internal error", err)
		return
	}
	c.JSON(http.StatusOK, dictionaryResp)
}
