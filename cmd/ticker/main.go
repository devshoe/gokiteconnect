package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"

	credentials "github.com/devshoe/gokiteconnect/credentials"
	"github.com/devshoe/gokiteconnect/models"
	kiteticker "github.com/devshoe/gokiteconnect/ticker"
)

var (
	// Nifty 50 instrument token
	tokenNifty50 uint32 = 256265
	username     string = ""
	password     string = ""
	twofa        string = ""
	apiKey       string = ""
	apiSecret    string = ""
)

func main() {
	z, err := credentials.NewUser(credentials.Credentials{
		UserID:     username,
		Password:   password,
		TOTPSecret: twofa,
		APIKey:     apiKey,
		APISecret:  apiSecret,
		IsAPIUser:  apiKey != "" && apiSecret != "",
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := z.Login(); err != nil {
		log.Fatal(err)
	}

	ticker := z.KiteTicker()
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
