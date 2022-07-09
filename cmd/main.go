package main

import (
	"game-prices-api/internal/api"
	"log"

	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	// Create API manager and run it
	manager := api.CreateAPIManager()
	err := manager.RunManager()
	if err != nil {
		log.Fatal(err.Error())
	}
}
