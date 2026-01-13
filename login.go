package kiteconnect

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// GetZerodhaEncToken returns encToken, which may be used to generate new clients and tickers
func GetZerodhaEncToken(username, password, twofa string) (string, error) {
	var cookies, _ = cookiejar.New(nil)
	var httpClient = http.Client{Jar: cookies}

	loginFormValues := make(url.Values)
	loginFormValues.Set("user_id", username)
	loginFormValues.Set("password", password)
	resp, _ := httpClient.PostForm("https://kite.zerodha.com/api/login", loginFormValues)
	var loginResponse map[string]map[string]string
	loginRespText, _ := io.ReadAll(resp.Body)
	json.Unmarshal(loginRespText, &loginResponse)
	requestID := loginResponse["data"]["request_id"]

	twofaFormValues := make(url.Values)
	twofaFormValues.Set("user_id", username)
	twofaFormValues.Set("twofa_value", twofa)
	twofaFormValues.Set("request_id", requestID)
	resp, _ = httpClient.PostForm("https://kite.zerodha.com/api/twofa", twofaFormValues)

	//publicToken := strings.Split(strings.Split(resp.Cookies()[0].String(), ";")[0], "public_token=")[1]
	cookieResp := resp.Cookies()
	if len(cookieResp) < 3 {
		return "", fmt.Errorf("(Kiteconnect.GetZerodhaEncToken) failed - probably wrong credentials (cookie parsing error)")
	}
	base := strings.Split(cookieResp[2].String(), ";")[0]
	encTokenSplit := strings.Split(base, "enctoken=")
	if len(encTokenSplit) < 2 {
		return "", fmt.Errorf("(Kiteconnect.GetZerodhaEncToken) failed - enctoken parsing failed for %s", encTokenSplit)
	}

	return encTokenSplit[1], nil
}

func GetZerodhaAccessToken(username, password, twofa, apiKey, apiSecret string) (string, error) {
	var cookies, _ = cookiejar.New(nil)
	var httpClient = http.Client{Jar: cookies}

	loginFormValues := make(url.Values)
	loginFormValues.Set("user_id", username)
	loginFormValues.Set("password", password)
	resp, _ := httpClient.PostForm("https://kite.zerodha.com/api/login", loginFormValues)
	var loginResponse map[string]map[string]string
	loginRespText, _ := io.ReadAll(resp.Body)
	json.Unmarshal(loginRespText, &loginResponse)
	requestID := loginResponse["data"]["request_id"]

	twofaFormValues := make(url.Values)
	twofaFormValues.Set("user_id", username)
	twofaFormValues.Set("twofa_value", twofa)
	twofaFormValues.Set("request_id", requestID)
	resp, _ = httpClient.PostForm("https://kite.zerodha.com/api/twofa", twofaFormValues)
	getResp, err := httpClient.Get("https://kite.trade/connect/login?api_key=" + apiKey)
	if err != nil {
		return "", fmt.Errorf("(Kiteconnect.GetZerodhaAccessToken) requestToken generation failed - probably wrong credentials (cookie parsing error)")
	}
	splitURL := strings.Split(getResp.Request.URL.String(), "request_token=")
	if len(splitURL) == 1 {
		return "", fmt.Errorf("(Kiteconnect.GetZerodhaAccessToken) request token retrieval failed - check for subscription or if authorized for first use")
	}
	requestToken := strings.Split(splitURL[1], "&")
	client := New(apiKey)
	data, err := client.GenerateSession(requestToken[0], apiSecret)
	if err != nil {
		return "", fmt.Errorf("(Kiteconnect.GetZerodhaAccessToken) kite session gen failed with request token - %s", err)
	}
	return data.AccessToken, nil
}
