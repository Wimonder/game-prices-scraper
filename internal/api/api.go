package api

import (
	"fmt"
	"game-prices-api/internal/scraper"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

type APIManager struct {
	Port    string
	scraper *scraper.Scraper
	engine  *gin.Engine
}

func ApiMiddleware(manager *APIManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("manager", manager)
		c.Next()
	}
}

func CreateAPIManager() *APIManager {
	apiManager := &APIManager{}

	// Creates a gin router with default middleware:
	// logger and recovery (crash-free) middleware
	apiManager.engine = gin.Default()

	// Setup the middleware which passes a reference to the manager
	apiManager.engine.Use(ApiMiddleware(apiManager))

	// Setup the routes
	apiManager.createRoutes()

	// Setup the scraper
	apiManager.scraper = scraper.CreateScraper()

	// Setup port
	// By default it serves on :8080 unless a
	// PORT environment variable was defined.
	apiManager.Port = fmt.Sprintf(":%v", os.Getenv("PORT"))

	return apiManager
}

func (manager *APIManager) RunManager() error {
	return manager.engine.Run(manager.Port)
}

func (manager *APIManager) createRoutes() {
	manager.engine.GET("/healthcheck", func(context *gin.Context) {
		context.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
		return
	})
	manager.engine.GET("/regions", getRegions)
	manager.engine.GET("/games", getGames)
	manager.engine.GET("/game/:title", getGame)
}

func getRegions(context *gin.Context) {
	logrus.Infoln("Getting regions")

	// Get the manager
	manager, err := getManagerFromContext(context)
	if err != nil {
		context.Status(http.StatusInternalServerError)
		return // Return early if we can't get the manager
	}

	// Scrape the regions
	regions, err := manager.scraper.ScrapeRegions()
	if err != nil {
		context.Status(http.StatusInternalServerError)
		return // Return early if we can't get the regions
	}

	// Set the response
	context.JSON(http.StatusOK, gin.H{
		"regions": regions,
	})
}

const DefaultLimit = "10"

func getGames(context *gin.Context) {
	// Get the manager
	manager, err := getManagerFromContext(context)
	if err != nil {
		context.Status(http.StatusInternalServerError)
		return // Return early if we can't get the manager
	}

	// Get query params
	region := context.DefaultQuery("region", "us")
	region = strings.ToLower(region)
	limit, limitError := strconv.ParseInt(context.DefaultQuery("limit", DefaultLimit), 10, 64)
	offset, offsetError := strconv.ParseInt(context.DefaultQuery("offset", "0"), 10, 64)
	title := context.Query("title")
	if title == "" || limitError != nil || offsetError != nil {
		context.Status(http.StatusBadRequest)
		return
	}
	logrus.Infoln(fmt.Sprintf("Getting games with query params: %s %s %d %d", title, region, limit, offset))

	// Scrape the games
	result, err := manager.scraper.ScrapeGames(title, region, int(limit), int(offset))
	if err != nil {
		context.Status(http.StatusInternalServerError)
		return
	}

	// Set the response
	context.JSON(http.StatusOK, gin.H{
		"totalAmountFound": result.TotalAmount,
		"amount":           result.Amount,
		"games":            result.Games,
	})
}

func getGame(context *gin.Context) {
	// Get the manager
	manager, err := getManagerFromContext(context)
	if err != nil {
		context.Status(http.StatusInternalServerError)
		return // Return early if we can't get the manager
	}

	// Get path and query params
	title := context.Param("title")
	gameType := context.DefaultQuery("type", "game")
	region := context.DefaultQuery("region", "us")
	region = strings.ToLower(region)
	logrus.Infoln(fmt.Sprintf("Getting game with query params: %s %s %s", title, gameType, region))

	// Scrape the game
	result, err := manager.scraper.ScrapeGame(title, region, gameType)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "404"):
			context.JSON(http.StatusNotFound, gin.H{})
			return
		default:
			context.JSON(http.StatusInternalServerError, gin.H{})
			return
		}
	}

	// Set the response
	context.JSON(http.StatusOK, result)
}
