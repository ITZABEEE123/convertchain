package main

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		dbURL := os.Getenv("DB_URL")

		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"db_url_present": dbURL != "",
		})
	})

	r.Run(":9000")
}