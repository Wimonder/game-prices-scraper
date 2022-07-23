package scraper

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gocolly/colly"
	"github.com/sirupsen/logrus"
)

type Scraper struct {
	BaseURL       string
	baseCollector *colly.Collector
}

type CollectedGames struct {
	Games       []CollectedGame `json:"games"`
	Amount      int             `json:"amount"`
	TotalAmount int             `json:"totalAmount"`
}

type CollectedGame = gin.H

type GameChannelElement struct {
	CollectedGame CollectedGame
	Index         int
	Error         error
}

func CreateScraper() *Scraper {
	scraper := &Scraper{}

	// Setup base url for the scraper
	scraper.BaseURL = os.Getenv("BASE_URL")

	// Create colly collector
	scraper.baseCollector = colly.NewCollector()
	// Collector behaviour
	// Print visited links
	scraper.baseCollector.OnRequest(func(r *colly.Request) {
		logrus.Infoln("Visiting", r.URL)
	})
	scraper.baseCollector.AllowURLRevisit = true

	return scraper
}

func (scraper *Scraper) ScrapeRegions() ([]string, error) {
	logrus.Infoln("Scraping regions")
	collector := scraper.baseCollector.Clone()

	regions := make([]string, 0)

	collector.OnHTML("div#settings-menu-region span.settings-menu-select-option-link", func(e *colly.HTMLElement) {
		regionUrl := e.Attr("onclick")
		logrus.Infoln(regionUrl)
		regionParts := strings.Split(regionUrl, "=")
		region := strings.Split(regionParts[1], "/")[1]
		logrus.Infoln(region)
		regions = append(regions, region)
	})

	err := collector.Visit(scraper.BaseURL)
	if err != nil {
		return nil, err
	}
	return regions, nil
}

const CHANNEL_SIZE = 100

func (scraper *Scraper) ScrapeGames(title string, region string, limit int, offset int) (CollectedGames, error) {
	logrus.Infoln("Scraping games")
	collector := scraper.baseCollector.Clone()

	// Set region
	regionUrl := fmt.Sprintf("%v/%v/region/switch/?return=%%2F", scraper.BaseURL, region)
	collector.Visit(regionUrl)
	// Collect all the games
	gameChannel := make(chan *GameChannelElement, CHANNEL_SIZE)
	// Check how many games there are and how many games per page
	gamesUrl := fmt.Sprintf("%v/games/?title=%v&view=list", scraper.BaseURL, url.QueryEscape(title))
	var amountFound int
	var gamesPerPage int
	collector.OnHTML("span.search-results-counter", func(e *colly.HTMLElement) {
		results := e.ChildText("span.value")
		amountFound, _ = strconv.Atoi(strings.Split(results, " ")[0])
	})
	collector.OnHTML("div.list-items", func(e *colly.HTMLElement) {
		e.ForEach("div.game-list-item", func(index int, e *colly.HTMLElement) {
			gamesPerPage += 1
		})
	})
	err := collector.Visit(gamesUrl)
	logrus.Infoln("Games per page", gamesPerPage)
	logrus.Infoln("Amount found", amountFound)
	logrus.Infoln("Games page url", gamesUrl)
	if err != nil {
		return CollectedGames{}, err
	}
	// If no games, return early
	if amountFound == 0 {
		return CollectedGames{}, nil
	}
	// Calculate amount of pages
	numberOfPages := amountFound / gamesPerPage
	// Calculate current page from offset
	startingPage := offset / gamesPerPage
	startingGameOffset := offset % gamesPerPage

	// Now collect all the games individually

	// Counter of the visited games
	var counter int
	var done bool
	collector.OnHTML("div.list-items", func(e *colly.HTMLElement) {
		e.ForEach("div.game-list-item", func(index int, e *colly.HTMLElement) {
			if index >= startingGameOffset && counter < limit {
				// Collect game
				gameUrl := e.ChildAttr("a.full-link", "href")
				gameParts := strings.Split(gameUrl, "/")
				gameType := gameParts[1]
				if gameType != "" {
					game := gameParts[2]
					logrus.Infoln(game, gameType)
					go scraper.collectGame(region, game, gameType, gameChannel, counter)
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
	// Keep visiting pages until we receive signal to stop
	if !done {
		currentPage := startingPage
		for !done && currentPage <= (numberOfPages+1) {
			logrus.Infof("visiting page: %v, current counter: %v", currentPage, counter)
			collector.Visit(fmt.Sprintf("%v&page=%v", gamesUrl, currentPage))
			currentPage++
		}
	}
	// All games collected
	logrus.Infof("Games visited: %v", counter)
	receivedCounter := 0
	games := make([]CollectedGame, counter)
	for receivedCounter < counter {
		// TODO: Handle error
		collectedGameElement := <-gameChannel
		games[collectedGameElement.Index] = collectedGameElement.CollectedGame
		receivedCounter++
	}

	return CollectedGames{
		Games:       games,
		Amount:      counter,
		TotalAmount: amountFound,
	}, nil
}

func (scraper *Scraper) ScrapeGame(title string, region string, gameType string) (CollectedGame, error) {
	logrus.Infoln("Scraping game")
	collector := scraper.baseCollector.Clone()

	// Set region
	regionUrl := fmt.Sprintf("%v/%v/region/switch/?return=%%2F", scraper.BaseURL, region)
	collector.Visit(regionUrl)
	// Collect game data
	gameChannel := make(chan *GameChannelElement)
	go scraper.collectGame(region, title, gameType, gameChannel, 0)
	collectedGameElement := <-gameChannel

	return collectedGameElement.CollectedGame, collectedGameElement.Error
}

var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func (scraper *Scraper) collectGame(region, id, gameType string, gameChannel chan *GameChannelElement, index int) {
	// TODO: Handle different types individually
	collector := scraper.baseCollector.Clone()
	// Collect game data
	gameUrl := fmt.Sprintf("%v/%v/%v/", scraper.BaseURL, gameType, id)
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
			// TODO: Handle error
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
			gameData["availability"] = resultOffers["availability"]
			resultOffersOffers := resultOffers["offers"].([]interface{})
			for _, offer := range resultOffersOffers {
				offerI := offer.(map[string]interface{})
				store := gin.H{}
				store["price"] = offerI["price"]
				store["seller"] = offerI["seller"].(map[string]interface{})["name"]
				// Resolve url
				location := resolveRedirectURL(offerI["url"].(string))
				store["url"] = location
				gameData["stores"] = append(gameData["stores"].([]gin.H), store)
			}
			logrus.Infof("Done processing game: %v", gameData["name"])
		}
	})

	collector.Visit(gameUrl)
	gameChannel <- &GameChannelElement{
		CollectedGame: gameData,
		Error:         err,
		Index:         index,
	}
}
