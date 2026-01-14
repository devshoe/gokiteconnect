package main

import (
	"fmt"

	"github.com/devshoe/gokiteconnect/zerodha"
)

var (
	username  = ""
	password  = ""
	twofa     = ""
	apiKey    = ""
	apiSecret = ""
)

func main() {
	z := zerodha.NewLoginClient(
		zerodha.WithWebUserCredentials(username, password, twofa),
		zerodha.WithAPIUserCredentials(apiKey, apiSecret),
		zerodha.WithEncToken(""),
		zerodha.WithAccessToken(""),
	)

	fmt.Println(z.LoginIfRequired())

	// z.KiteTicker().Serve()
}
