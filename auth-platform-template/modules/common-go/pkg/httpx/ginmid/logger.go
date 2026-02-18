package ginmid

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		lat := time.Since(start).Milliseconds()
		rid, _ := c.Get("request_id")
		uid, _ := c.Get("uid")
		tid, _ := c.Get("tid")
		log.Printf("request_id=%v uid=%v tid=%v method=%s path=%s status=%d latency_ms=%d",
			rid, uid, tid, c.Request.Method, c.FullPath(), c.Writer.Status(), lat)
	}
}
