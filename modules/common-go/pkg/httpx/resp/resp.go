package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Envelope struct {
	RequestID string `json:"request_id"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
}

func requestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{RequestID: requestID(c), Code: 0, Message: "ok", Data: data})
}

func Fail(c *gin.Context, httpStatus int, code int, message string, data any) {
	c.JSON(httpStatus, Envelope{RequestID: requestID(c), Code: code, Message: message, Data: data})
}
