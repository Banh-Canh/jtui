// Package jellyfin provides a developer-friendly Go client for the Jellyfin API
package jellyfin

import (
	"encoding/json"
	"fmt"
	"io"
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
	client.Download = &DownloadAPI{
		client: client,
		Queue:  NewDownloadQueue(),
		downloadHTTP: &http.Client{
			Timeout: 0, // no timeout for large file transfers
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
				ForceAttemptHTTP2:   false, // use HTTP/1.1 to avoid stream resets
			},
		},
	}

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

// doRequest creates and executes an authenticated HTTP request, returning the response body.
// It sets the full MediaBrowser authorization header and Content-Type.
// The caller is responsible for providing the correct method, URL, and optional body.
func (c *Client) doRequest(method, url string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		c.config.ClientName,
		c.config.ClientName,
		c.config.DeviceID,
		c.config.Version,
		c.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// doRequestDecode creates and executes an authenticated HTTP request, decoding the JSON response into dest.
func (c *Client) doRequestDecode(method, url string, body io.Reader, dest interface{}) error {
	respBody, err := c.doRequest(method, url, body)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(respBody, dest); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// doTokenRequest creates and executes an HTTP request with the simple Token authorization header.
// Used by libraries and auth endpoints that use the shorter auth format.
func (c *Client) doTokenRequest(method, url string) ([]byte, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", c.config.AccessToken))
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", c.config.ClientName, c.config.Version))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return body, nil
}

// toItems converts a slice of concrete item types to a slice of the Item interface.
func toItems[T Item](items []T) []Item {
	result := make([]Item, len(items))
	for i, item := range items {
		result[i] = item
	}
	return result
}
