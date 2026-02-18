package ginmid

import (
	"github.com/gin-gonic/gin"

	"anvilkit-auth-template/modules/common-go/pkg/httpx/apperr"
	"anvilkit-auth-template/modules/common-go/pkg/httpx/resp"
)

func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		ae := apperr.Normalize(c.Errors.Last().Err)
		data := ae.Data
		if data == nil {
			data = map[string]any{}
		}
		resp.Fail(c, ae.HTTPStatus, ae.Code, ae.Message, data)
	}
}
