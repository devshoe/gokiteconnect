package zerodha

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	kiteconnect "github.com/devshoe/gokiteconnect"
	kiteticker "github.com/devshoe/gokiteconnect/ticker"
	"github.com/pquerna/otp/totp"
)

type loginResponse struct {
	Status string `json:"status"`
	Data   struct {
		UserID      string   `json:"user_id"`
		RequestID   string   `json:"request_id"`
		TwoFAType   string   `json:"twofa_type"`
		TwoFATypes  []string `json:"twofa_types"`
		TwoFAStatus string   `json:"twofa_status"`
		Profile     struct {
			UserName      string `json:"user_name"`
			UserShortName string `json:"user_shortname"`
			AvatarURL     string `json:"avatar_url"`
		} `json:"profile"`
		TwoFAValue string `json:"twofa_value"`
	} `json:"data"`
}

type twofaResponse struct {
	Data struct {
		AttemptsRemaining int `json:"attempts_remaining"`
	}
	ErrorType string `json:"error_type"`
	Message   string `json:"message"`
	Status    string `json:"status"`
}

// LoginClient is a struct that contains all the necessary information to make requests to Zerodha
// and keep track of the login state
type LoginClient struct {
	httpClient       http.Client
	loginResponse    loginResponse
	twofaResponse    twofaResponse
	baseLoginCookies []*http.Cookie

	username   string
	password   string
	totpSecret string
	apiKey     string
	apiSecret  string

	EncToken     string
	RequestToken string
	AccessToken  string

	loggedIn bool
}

// LoginClientOption is used to modify ZerodhaLoginClient
type LoginClientOption func(*LoginClient)

// WithWebUserCredentials is used to set the web user credentials: if you need market data for free
func WithWebUserCredentials(username, password, totpSecret string) LoginClientOption {
	return func(z *LoginClient) {
		z.username = username
		z.password = password
		z.totpSecret = totpSecret
	}
}

// WithAPIUserCredentials is used to set the API user credentials: if you are subscribed to the premium plan
func WithAPIUserCredentials(apiKey, apiSecret string) LoginClientOption {
	return func(z *LoginClient) {
		z.apiKey = apiKey
		z.apiSecret = apiSecret
	}
}

// WithEncToken is used to set the enctoken: if you are already logged in
func WithEncToken(enctoken string) LoginClientOption {
	return func(z *LoginClient) {
		z.EncToken = enctoken
	}
}

// WithAccessToken is used to set the accessToken: if you are already logged in
func WithAccessToken(accessToken string) LoginClientOption {
	return func(z *LoginClient) {
		z.AccessToken = accessToken
	}
}

// NewLoginClient creates a new ZerodhaLoginClient
func NewLoginClient(options ...LoginClientOption) *LoginClient {
	cookies, _ := cookiejar.New(nil)
	zlc := &LoginClient{
		httpClient: http.Client{Jar: cookies},
	}
	for _, option := range options {
		option(zlc)
	}

	if zlc.username == "" || zlc.password == "" || zlc.totpSecret == "" {
		panic("one of username, password or totpSecret is missing")
	}
	return zlc
}

// LoginIfRequired checks if the enctoken or accessToken is valid, attempts a login otherwise
func (lc *LoginClient) LoginIfRequired() (bool, error) {
	_, err := lc.KiteClient().GetUserProfile()
	if err == nil {
		return false, nil
	}

	if err := lc.Login(); err != nil {
		return false, err
	}

	return true, nil
}

// Login force logs in to Zerodha and stores all cookies
func (lc *LoginClient) Login() error {
	var (
		baseLoginURL = "https://kite.zerodha.com/api/login"
		baseTwofaURL = "https://kite.zerodha.com/api/twofa"
		err          error
		resp         *http.Response
	)

	loginFormValues := make(url.Values)
	loginFormValues.Set("user_id", lc.username)
	loginFormValues.Set("password", lc.password)

	if resp, err = lc.httpClient.PostForm(baseLoginURL, loginFormValues); err != nil {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed to post login request: %w", err)
	} else if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed login - wrong credentials (status code %d)", resp.StatusCode)
	} else if err = json.NewDecoder(resp.Body).Decode(&lc.loginResponse); err != nil {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed to decode login response: %w", err)
	} else if lc.loginResponse.Status != "success" {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed login- unsucessful status of %s", lc.loginResponse.Status)
	}

	twofaFormValues := make(url.Values)
	twofaFormValues.Set("user_id", lc.username)
	twofaFormValues.Set("twofa_value", lc.getTOTP())
	twofaFormValues.Set("request_id", lc.loginResponse.Data.RequestID)

	if resp, err = lc.httpClient.PostForm(baseTwofaURL, twofaFormValues); err != nil {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed to post twofa request: %w", err)
	} else if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed twofa - wrong credentials (status code %d)", resp.StatusCode)
	} else if err = json.NewDecoder(resp.Body).Decode(&lc.twofaResponse); err != nil {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed to decode twofa response: %w", err)
	} else if lc.twofaResponse.Status != "success" {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed twofa- unsucessful status of %s", lc.twofaResponse.Status)
	}

	lc.baseLoginCookies = resp.Cookies()
	lc.loggedIn = true

	//TODO: only run these when enctoken and accessToken are not set
	if err = lc.setEncToken(); err != nil {
		return fmt.Errorf("(ZerodhaLoginClient.Login) failed to set enctoken: %w", err)
	}

	if lc.isAPIUser() {
		if err = lc.setAccessToken(); err != nil {
			return fmt.Errorf("(ZerodhaLoginClient.Login) failed to set access token: %w", err)
		}
	}

	return nil
}

func (lc *LoginClient) getKiteHTTPClient(timeoutSeconds int) *http.Client {
	return &http.Client{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   10,
			ResponseHeaderTimeout: time.Second * time.Duration(timeoutSeconds),
		},
	}
}

// KiteClient returns a new KiteClient
func (lc *LoginClient) KiteClient() *kiteconnect.Client {
	c := kiteconnect.New(lc.apiKey)
	c.SetHTTPClient(lc.getKiteHTTPClient(1))
	if lc.isAPIUser() {
		c.SetAccessToken(lc.AccessToken)
	} else {
		c.SetEncToken(lc.EncToken)
	}
	return c
}

// KiteTicker returns a new KiteTicker
func (lc *LoginClient) KiteTicker() *kiteticker.Ticker {
	tkr := kiteticker.New(lc.apiKey, lc.AccessToken)
	if !lc.isAPIUser() {
		tkr.SetEncToken(lc.username, lc.EncToken)
	}
	return tkr
}

// setEncToken sets the enctoken
func (lc *LoginClient) setEncToken() error {
	if !lc.loggedIn {
		return fmt.Errorf("(ZerodhaLoginClient.EncToken) not logged in, call Login() first")
	}

	for _, cookie := range lc.baseLoginCookies {
		if cookie.Name == "enctoken" {
			lc.EncToken = cookie.Value
			return nil
		}
	}
	return fmt.Errorf("(ZerodhaLoginClient.EncToken) enctoken not found")
}

// setAccessToken sets the accessToken, only useful if you have api key and secret
func (lc *LoginClient) setAccessToken() error {
	var (
		resp                *http.Response
		err                 error
		session             kiteconnect.UserSession
		baseRequestTokenURL = fmt.Sprintf("https://kite.trade/connect/login?api_key=%s", lc.apiKey)
		firstTimeAuthURL    = fmt.Sprintf("https://kite.zerodha.com/connect/login?v=3&api_key=%s", lc.apiKey)
		requestToken        string
	)

	if !lc.loggedIn {
		return fmt.Errorf("(ZerodhaLoginClient.AccessToken) not logged in, call Login() first")
	} else if resp, err = lc.httpClient.Get(baseRequestTokenURL); err != nil {
		return fmt.Errorf("(ZerodhaLoginClient.AccessToken) requestToken generation failed - %w", err)
	} else if strings.Contains(resp.Request.URL.String(), "authorize") { // first usage requires permission from the user
		return fmt.Errorf("(ZerodhaLoginClient.AccessToken) First time authorization required: visit %s", firstTimeAuthURL)
	} else if requestToken = resp.Request.URL.Query().Get("request_token"); requestToken == "" {
		return fmt.Errorf("(ZerodhaLoginClient.AccessToken) request token retrieval failed - check for subscription or if authorized for first use")
	} else if session, err = kiteconnect.New(lc.apiKey).GenerateSession(requestToken, lc.apiSecret); err != nil {
		return fmt.Errorf("(ZerodhaLoginClient.AccessToken) kite session gen failed with request token - %w", err)
	}

	lc.RequestToken = requestToken
	lc.AccessToken = session.AccessToken
	return nil
}

func (lc *LoginClient) isAPIUser() bool {
	return lc.apiKey != "" || lc.apiSecret != ""
}

func (lc *LoginClient) getTOTP() string {
	totp, err := totp.GenerateCode(lc.totpSecret, time.Now())
	if err != nil {
		panic(err)
	}
	return totp
}
