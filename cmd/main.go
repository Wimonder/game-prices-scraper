package main

import (
	"game-prices-api/internal/api"
	"log"

	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	// Create API manager and run it
	manager := api.CreateAPIManager()
	err = manager.RunManager()
	if err != nil {
		log.Fatal(err.Error())
	}
}

