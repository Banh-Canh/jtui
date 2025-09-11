// Package jellyfin provides a developer-friendly Go client for the Jellyfin API
package jellyfin

import (
	"fmt"
	"net/http"
	"time"
)

// Client is the main Jellyfin API client
type Client struct {
	config *Config
	http   *http.Client
	
	// API modules
	Auth      *AuthAPI
	Libraries *LibrariesAPI
	Items     *ItemsAPI
	Playback  *PlaybackAPI
	Search    *SearchAPI
	Download  *DownloadAPI
}

// Config holds the client configuration
type Config struct {
	ServerURL   string
	AccessToken string
	UserID      string
	DeviceID    string
	ClientName  string
	Version     string
	Timeout     time.Duration
}

// NewClient creates a new Jellyfin client with the given configuration
func NewClient(config *Config) *Client {
	if config.ClientName == "" {
		config.ClientName = "jtui"
	}
	if config.Version == "" {
		config.Version = "1.0.0"
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}

	// Optimized HTTP client with enhanced connection pooling
	transport := &http.Transport{
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   10,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
		ForceAttemptHTTP2:     true,
	}

	httpClient := &http.Client{
		Timeout:   config.Timeout,
		Transport: transport,
	}

	client := &Client{
		config: config,
		http:   httpClient,
	}

	// Initialize API modules
	client.Auth = &AuthAPI{client: client}
	client.Libraries = &LibrariesAPI{client: client}
	client.Items = &ItemsAPI{client: client}
	client.Playback = &PlaybackAPI{client: client}
	client.Search = &SearchAPI{client: client}
	client.Download = &DownloadAPI{client: client}

	return client
}

// GetConfig returns the client configuration
func (c *Client) GetConfig() *Config {
	return c.config
}

// GetHTTPClient returns the underlying HTTP client
func (c *Client) GetHTTPClient() *http.Client {
	return c.http
}

// SetAccessToken updates the access token
func (c *Client) SetAccessToken(token string) {
	c.config.AccessToken = token
}

// SetUserID updates the user ID
func (c *Client) SetUserID(userID string) {
	c.config.UserID = userID
}

// SetDeviceID updates the device ID
func (c *Client) SetDeviceID(deviceID string) {
	c.config.DeviceID = deviceID
}

// IsAuthenticated checks if the client has authentication credentials
func (c *Client) IsAuthenticated() bool {
	return c.config.AccessToken != "" && c.config.UserID != ""
}

// GetAuthHeader returns the MediaBrowser authorization header
func (c *Client) GetAuthHeader() string {
	return fmt.Sprintf(`MediaBrowser Client="%s", Device="CLI", DeviceId="%s", Version="%s"`,
		c.config.ClientName, c.config.DeviceID, c.config.Version)
}

// GetTokenHeader returns the X-Emby-Token header value
func (c *Client) GetTokenHeader() string {
	return c.config.AccessToken
}