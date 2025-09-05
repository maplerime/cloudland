/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0

*/

package common

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
)

// type JSONTime time.Time

type BaseReference struct {
	ID   string `json:"id" binding:"omitempty,uuid"`
	Name string `json:"name" binding:"omitempty,min=2,max=36"`
}

type ResourceReference struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Owner     string `json:"owner,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type BaseID struct {
	ID string `json:"id" binding:"required,uuid"`
}

type APIError struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

func ErrorResponse(c *gin.Context, code int, errorMsg string, err error) {
	logger.Errorf("%s, %v\n", errorMsg, err)
	if err != nil {
		var clErr *CLError
		if errors.As(err, &clErr) {
			c.JSON(code, &APIError{
				ErrorCode:    clErr.Code,
				ErrorMessage: clErr.Message,
			})
			return
		}
		errorMsg = errorMsg + ": " + err.Error()
	}
	c.JSON(code, &APIError{
		ErrorCode:    code,
		ErrorMessage: errorMsg,
	})
}

func (e *APIError) Error() string {
	return fmt.Sprintf("CLError: code=%d, message=%s", e.ErrorCode, e.ErrorMessage)
}

/*
type Marshaler interface {
    MarshalJSON() ([]byte, error)
}

func (t JSONTime)MarshalJSON() ([]byte, error) {
    //do your serializing here
    stamp := fmt.Sprintf("\"%s\"", time.Time(t).Format("Mon Jan _2"))
    return []byte(stamp), nil
}
*/
