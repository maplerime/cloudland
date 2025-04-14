package apis

import (
	"context"
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
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DictionaryAPI struct{}

func (v *DictionaryAPI) List(c *gin.Context) {
	ctx := c.Request.Context()
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "50")
	offset, err := strconv.Atoi(offsetStr)
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
	total, dictionaries, err := dictionaryAdmin.List(ctx, int64(offset), int64(limit), "-created_at", "")
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Failed to list dictionaries", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"total":        total,
		"offset":       offset,
		"limit":        limit,
		"dictionaries": dictionaries,
	})
}

func (v *DictionaryAPI) Create(c *gin.Context) {
	ctx := c.Request.Context()
	payload := &model.Dictionary{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}

	var dictionary *model.Dictionary
	dictionary, err = dictionaryAdmin.Create(ctx, payload.Type, payload.Name, payload.Value)
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
	dictionaryResp = &DictionaryResponse{
		Name:  dictionary.Name,
		Type:  dictionary.Type,
		Value: dictionary.Value,
	}
	return
}

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

func (v *DictionaryAPI) Patch(c *gin.Context) {
	ctx := c.Request.Context()
	uuID := c.Param("id")

	payload := &model.Dictionary{}
	err := c.ShouldBindJSON(payload)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid input JSON", err)
		return
	}
	dictionaries, err := dictionaryAdmin.GetDictionaryByUUID(ctx, uuID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, "Invalid subnet query", err)
		return
	}
	var dictionary *model.Dictionary

	dictionary, err = dictionaryAdmin.Update(ctx, dictionaries, payload.Type, payload.Name, payload.Value)
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
