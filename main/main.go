package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gocolly/colly/v2"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	// Creates a gin router with default middleware:
	// logger and recovery (crash-free) middleware
	router := gin.Default()

	router.GET("/regions", getRegions)
	router.GET("/games", getGames)
	router.GET("/game/:title", getGame)

	// By default it serves on :8080 unless a
	// PORT environment variable was defined.
	router.Run(fmt.Sprintf(":%v", os.Getenv("PORT")))
}

const BaseUrl = "https://gg.deals"

func getRegions(context *gin.Context) {
	logrus.Infoln("Getting regions")
	collector := makeCollector()

	regions := make([]string, 0)

	collector.OnHTML("a.settings-menu-select-option-link", func(e *colly.HTMLElement) {
		regionUrl := e.Attr("href")
		regionParts := strings.Split(regionUrl, "/")
		if regionParts[2] == "region" {
			region := regionParts[1]
			logrus.Infoln(region)
			regions = append(regions, region)
		}
	})

	collector.Visit(BaseUrl)

	context.JSON(http.StatusOK, gin.H{
		"regions": regions,
	})
}

const DefaultLimit = "10"

func getGames(context *gin.Context) {
	region := context.DefaultQuery("region", "us")
	limit, _ := strconv.ParseInt(context.DefaultQuery("limit", DefaultLimit), 10, 64)
	offset, _ := strconv.ParseInt(context.DefaultQuery("offset", "0"), 10, 64)
	title := context.Query("title")

	logrus.Infoln("Getting games")
	collector := makeCollector()
	// First set region
	regionUrl := fmt.Sprintf("%v/%v/region/switch/?return=%%2F", BaseUrl, region)
	collector.Visit(regionUrl)
	// Collect all the games
	gameCh := make(chan *CollectedGame)
	errCh := make(chan error)
	// Check how many games their are to determine amount of pages
	gamesUrl := fmt.Sprintf("%v/games/?title=%v&view=list", BaseUrl, title)
	var amountFound int
	collector.OnHTML("span.search-results-counter", func(e *colly.HTMLElement) {
		results := e.ChildText("span.value")
		amountFound, _ = strconv.Atoi(strings.Split(results, " ")[0])
	})
	collector.Visit(gamesUrl)
	if amountFound == 0 {
		context.JSON(http.StatusOK, gin.H{
			"amountFound": amountFound,
			"games":       []gin.H{},
		})
		return
	}
	numberOfPages := amountFound / 24
	logrus.Infof("number of pages: %v", numberOfPages)
	// Calculate page from offset
	startingPage := offset / 24
	startingGameOffset := offset % 24

	var counter int
	var done bool
	collector.OnHTML("div.list-items", func(e *colly.HTMLElement) {
		e.ForEach("div.game-list-item", func(index int, e *colly.HTMLElement) {
			if int64(index) >= startingGameOffset && counter < int(limit) {
				// Collect game
				gameUrl := e.ChildAttr("a.game-hoverable.full-link", "href")
				gameParts := strings.Split(gameUrl, "/")
				gameType := gameParts[1]
				if gameType != "" {
					game := gameParts[2]
					logrus.Infoln(game, gameType)
					go collectGame(region, game, gameType, gameCh, errCh, counter)
					counter++
				} else {
					logrus.Warningln("could not collect", gameUrl)
				}
			} else if counter >= int(limit) {
				done = true
				return
			}
		})
	})

	// Visit all the pages from starting page
	if startingPage == 0 {
		collector.Visit(gamesUrl)
		// After visiting no offset is required anymore only the limit
		startingGameOffset = -1
		startingPage++
	}
	// Keep visiting pages until done
	if !done {
		currentPage := startingPage
		for !done && currentPage <= int64(numberOfPages+1) {
			logrus.Infof("visiting page: %v, current counter: %v", currentPage, counter)
			collector.Visit(fmt.Sprintf("%v&page=%v", gamesUrl, currentPage))
			currentPage++
		}
	}

	logrus.Infof("Games visited: %v", counter)
	receivedCounter := 0
	games := make([]gin.H, counter)
	for receivedCounter < counter {
		<-errCh
		game := <-gameCh
		games[game.index] = game.gameData
		receivedCounter++
	}

	context.JSON(http.StatusOK, gin.H{
		"totalAmountFound": amountFound,
		"amount":           counter,
		"games":            games,
	})
}

type CollectedGame struct {
	index    int
	gameData gin.H
}

func collectGame(region, id, gameType string, ch chan *CollectedGame, errCh chan error, index int) {
	// TODO: Handle different types differently
	collector := makeCollector()
	// Collect game data
	gameUrl := fmt.Sprintf("%v/%v/%v/", BaseUrl, gameType, id)
	gameData := gin.H{
		"id":   id,
		"type": gameType,
	}
	gameData["stores"] = make([]gin.H, 0)
	var err error

	// TODO: Parse tags etc.
	// Price info
	collector.OnHTML("script", func(e *colly.HTMLElement) {
		if e.Attr("type") == "application/ld+json" {
			var result map[string]interface{}
			json.Unmarshal([]byte(e.Text), &result)
			// Fill in data
			logrus.Infof("Started processing game: %v", result["name"])
			gameData["name"] = result["name"]
			gameData["releaseDate"] = result["productionDate"]
			gameBrand, ok := result["brand"]
			if ok {
				gameData["developer"] = gameBrand.(map[string]interface{})["name"]
			}

			resultOffers := result["offers"].(map[string]interface{})
			gameData["currency"] = resultOffers["priceCurrency"]
			gameData["currentLowestPrice"] = resultOffers["price"]
			resultOffersOffers := resultOffers["offers"].([]interface{})
			doneCh := make(chan uint8, len(resultOffersOffers))
			for _, offer := range resultOffersOffers {
				offerI := offer.(map[string]interface{})
				store := gin.H{}
				store["price"] = offerI["price"]
				store["seller"] = offerI["seller"].(map[string]interface{})["name"]
				// Resolve url
				go resolveRedirectUrl(offerI["url"].(string), &store, doneCh)
				gameData["stores"] = append(gameData["stores"].([]gin.H), store)
			}
			doneAmount := 0
			for doneAmount < len(resultOffersOffers) {
				<-doneCh
				doneAmount++
			}
			logrus.Infof("Done processing game: %v", gameData["name"])
		}
	})

	collector.OnResponseHeaders(func(r *colly.Response) {
		if r.StatusCode != 200 {
			err = fmt.Errorf("something went wrong, status code: %v", r.StatusCode)
		}
	})

	collector.Visit(gameUrl)
	errCh <- err
	ch <- &CollectedGame{index, gameData}
}

var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func resolveRedirectUrl(url string, data *gin.H, done chan uint8) {
	res, _ := client.Get(url)
	(*data)["url"] = res.Header.Get("Location")
	done <- 1
}

func getGame(context *gin.Context) {
	title := context.Param("title")
	gameType := context.DefaultQuery("type", "game")
	region := context.DefaultQuery("region", "us")
	collector := makeCollector()
	// First set region
	regionUrl := fmt.Sprintf("%v/%v/region/switch/?return=%%2F", BaseUrl, region)
	collector.Visit(regionUrl)
	// Then collect game data
	gameCh := make(chan *CollectedGame)
	errCh := make(chan error)
	go collectGame(region, title, gameType, gameCh, errCh, 0)
	err := <-errCh
	game := <-gameCh
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "404"):
			context.JSON(http.StatusNotFound, gin.H{
				"Message": "Game not found",
			})
			return
		case strings.Contains(err.Error(), "5"):
			context.JSON(http.StatusInternalServerError, gin.H{
				"Message": err.Error(),
			})
			return
		default:
		}
	}
	context.JSON(http.StatusOK, game.gameData)
}

var baseCollector *colly.Collector

func makeCollector() *colly.Collector {
	var collector *colly.Collector
	if baseCollector == nil {
		baseCollector = colly.NewCollector()

		// Printing visited links
		baseCollector.OnRequest(func(r *colly.Request) {
			logrus.Infoln("Visiting", r.URL)
		})

		collector = baseCollector
	} else {
		collector = baseCollector.Clone()
	}
	collector.AllowURLRevisit = true
	return collector
}
