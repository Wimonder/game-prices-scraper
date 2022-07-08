package api

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func getManagerFromContext(context *gin.Context) (*APIManager, error) {
	manager, ok := context.MustGet("manager").(*APIManager)
	if !ok {
		return nil, fmt.Errorf("could not get manager from context")
	}

	return manager, nil
}

