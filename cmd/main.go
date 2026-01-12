package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/zerodha/gokiteconnect/v4/models"
	kiteticker "github.com/zerodha/gokiteconnect/v4/ticker"
)

var (
	// Nifty 50 instrument token
	tokenNifty50 uint32 = 256265
)

func main() {
	enctoken := os.Getenv("ENCTOKEN")
	if enctoken == "" {
		// Fallback for convenience if not set
		enctoken = ""
	}

	// Initialize Ticker
	// API key and access token are not used when enctoken is set, but required by constructor
	ticker := kiteticker.New("my_api_key", "my_access_token")

	// Set enctoken
	ticker.SetEncToken(enctoken, "")

	// Callback for connection establishment
	ticker.OnConnect(func() {
		fmt.Println("Connected")
		fmt.Println("Subscribing to Nifty 50")
		if err := ticker.Subscribe([]uint32{tokenNifty50}); err != nil {
			fmt.Printf("Error subscribing: %v\n", err)
		}

		if err := ticker.SetMode(kiteticker.ModeFull, []uint32{tokenNifty50}); err != nil {
			fmt.Printf("Error setting mode: %v\n", err)
		}
	})

	// Callback for tick reception
	ticker.OnTick(func(tick models.Tick) {
		fmt.Printf("Tick: %+v\n", tick)
	})

	// Callback for error
	ticker.OnError(func(err error) {
		fmt.Printf("Error: %v\n", err)
	})

	// Callback for close
	ticker.OnClose(func(code int, reason string) {
		fmt.Printf("Closed: %d %s\n", code, reason)
	})

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		ticker.Stop()
		os.Exit(0)
	}()

	// Start the ticker
	fmt.Println("Starting ticker...")
	ticker.Serve()
}
