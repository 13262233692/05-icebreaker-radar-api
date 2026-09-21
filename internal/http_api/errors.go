package http_api

import "github.com/gin-gonic/gin"

type apiError struct{ msg string }

func (e apiError) Error() string { return e.msg }

func badRequest(msg string) error { return apiError{msg: msg} }

func abortOnError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(apiError); ok {
		c.JSON(400, gin.H{"error": err.Error()})
		return true
	}
	c.JSON(500, gin.H{"error": "internal error"})
	return true
}
