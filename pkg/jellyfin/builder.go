package jellyfin

import (
	"fmt"
	"time"
)

// ClientBuilder provides a fluent interface for creating Jellyfin clients
type ClientBuilder struct {
	config *Config
}

// NewClientBuilder creates a new client builder
func NewClientBuilder() *ClientBuilder {
	return &ClientBuilder{
		config: &Config{
			ClientName: "jtui",
			Version:    "1.0.0",
			Timeout:    10 * time.Second,
		},
	}
}

// WithServerURL sets the Jellyfin server URL
func (b *ClientBuilder) WithServerURL(url string) *ClientBuilder {
	b.config.ServerURL = url
	return b
}

// WithClientName sets the client name
func (b *ClientBuilder) WithClientName(name string) *ClientBuilder {
	b.config.ClientName = name
	return b
}

// WithVersion sets the client version
func (b *ClientBuilder) WithVersion(version string) *ClientBuilder {
	b.config.Version = version
	return b
}

// WithTimeout sets the HTTP timeout
func (b *ClientBuilder) WithTimeout(timeout time.Duration) *ClientBuilder {
	b.config.Timeout = timeout
	return b
}

// WithDeviceID sets the device ID
func (b *ClientBuilder) WithDeviceID(deviceID string) *ClientBuilder {
	b.config.DeviceID = deviceID
	return b
}

// WithCredentials sets the access token and user ID
func (b *ClientBuilder) WithCredentials(accessToken, userID string) *ClientBuilder {
	b.config.AccessToken = accessToken
	b.config.UserID = userID
	return b
}

// Build creates the client with the configured options
func (b *ClientBuilder) Build() (*Client, error) {
	if b.config.ServerURL == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	return NewClient(b.config), nil
}

// BuildAndConnect creates the client and performs authentication if needed
func (b *ClientBuilder) BuildAndConnect() (*Client, error) {
	client, err := b.Build()
	if err != nil {
		return nil, err
	}

	// Test connection first
	if err := client.Auth.TestConnection(); err != nil {
		return nil, fmt.Errorf("failed to connect to server: %v", err)
	}

	// If not authenticated, try to load session or authenticate
	if !client.IsAuthenticated() {
		// Try to load existing session
		if err := client.Auth.LoadSession(); err == nil {
			// Validate existing session
			if err := client.Auth.ValidateSession(); err == nil {
				return client, nil
			}
		}

		// Authenticate using Quick Connect
		if err := client.Auth.AuthenticateWithQuickConnect(); err != nil {
			return nil, fmt.Errorf("authentication failed: %v", err)
		}

		// Save session
		client.Auth.SaveSession() // Ignore error - not critical
	}

	return client, nil
}

// ConnectFromConfig creates a client from external configuration (like viper)
// Falls back to offline mode if server is unavailable
func ConnectFromConfig(getConfigString func(key string) string) (*Client, error) {
	serverURL := getConfigString("jellyfin.server_url")
	if serverURL == "" {
		return nil, fmt.Errorf("jellyfin.server_url must be configured")
	}

	// Try to connect normally first
	client, err := NewClientBuilder().
		WithServerURL(serverURL).
		BuildAndConnect()
	
	if err != nil {
		// If server connection fails, try offline mode
		return CreateOfflineClient(serverURL)
	}
	
	return client, nil
}

// CreateOfflineClient creates a client that works only with offline content
func CreateOfflineClient(serverURL string) (*Client, error) {
	config := &Config{
		ServerURL:  serverURL,
		ClientName: "jtui-offline",
		Version:    "1.0.0",
		Timeout:    10 * time.Second,
		// No AccessToken or UserID - indicates offline mode
	}

	client := NewClient(config)
	
	// Check if we have any offline content
	offlineItems, err := client.Download.DiscoverOfflineContent()
	if err != nil {
		return nil, fmt.Errorf("failed to discover offline content: %w", err)
	}
	
	if len(offlineItems) == 0 {
		return nil, fmt.Errorf("no server connection and no offline content available")
	}
	
	return client, nil
}

// IsOfflineMode checks if the client is running in offline mode
func (c *Client) IsOfflineMode() bool {
	return c.config.AccessToken == "" && c.config.UserID == ""
}