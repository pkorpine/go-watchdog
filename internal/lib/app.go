package lib

import (
	"log"
	"sync"
	"time"

	"github.com/labstack/echo"
)

type App struct {
	DB   *Database
	Rest *echo.Echo
}

func (a *App) Initialize(dbParameters, token, prefix, hmacSecret string) {
	// Database
	a.DB = NewDatabase(dbParameters)
	a.DB.Init()

	// REST Server
	a.Rest = NewRestServer(prefix, a.DB, hmacSecret)

	// Telegram Bot
	InitTelegram(token, a.DB)
}

func (a *App) Run(bindParameter string) {
	// Start goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	// Ticker
	ticker := time.NewTicker(3 * time.Second)
	go func() {
		for _ = range ticker.C {
			a.DB.ProcessExpiredTimers()
		}
	}()

	go func() {
		log.Println("Telegram bot start")
		startTelegram()
		log.Println("Telegram bot stop")
		wg.Done()
	}()

	go func() {
		log.Println("Rest server start")
		a.Rest.Logger.Fatal(a.Rest.Start(bindParameter))
		log.Println("Rest server stop")
		wg.Done()
	}()

	// Wait all to finish (should not happen)
	wg.Wait()

	log.Println("The end")
}

func (a *App) Exit() {
	a.DB.Close()
}
