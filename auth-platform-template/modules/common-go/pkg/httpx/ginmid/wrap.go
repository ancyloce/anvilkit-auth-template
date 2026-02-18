package ginmid

import "github.com/gin-gonic/gin"

type HandlerE func(*gin.Context) error

func Wrap(h HandlerE) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := h(c); err != nil {
			_ = c.Error(err)
		}
	}
}
