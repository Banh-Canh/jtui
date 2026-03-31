package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

// AuthAPI handles authentication-related operations
type AuthAPI struct {
	client *Client
}

// TestConnection tests basic connectivity to the Jellyfin server
func (a *AuthAPI) TestConnection() error {
	resp, err := a.client.http.Get(a.client.config.ServerURL + "/System/Info")
	if err != nil {
		return fmt.Errorf("basic HTTP test failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// CheckQuickConnectEnabled checks if Quick Connect is enabled on the server
func (a *AuthAPI) CheckQuickConnectEnabled() (bool, error) {
	url := fmt.Sprintf("%s/QuickConnect/Enabled", a.client.config.ServerURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", a.client.config.ClientName, a.client.config.Version))

	resp, err := a.client.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	return string(body) == "true", nil
}

// InitiateQuickConnect starts a Quick Connect authentication session
func (a *AuthAPI) InitiateQuickConnect() (*QuickConnectData, error) {
	url := fmt.Sprintf("%s/QuickConnect/Initiate", a.client.config.ServerURL)

	// Generate deviceId if not set
	if a.client.config.DeviceID == "" {
		a.client.config.DeviceID = fmt.Sprintf("%s-%d", a.client.config.ClientName, time.Now().Unix())
	}

	// Try optimized method order (most likely to succeed first)
	methods := []struct {
		name   string
		method string
		body   []byte
	}{
		{"POST method with empty JSON", "POST", []byte("{}")},
		{"POST method with no body", "POST", nil},
		{"GET method (legacy)", "GET", nil},
	}

	var lastErr error
	for _, method := range methods {
		result, err := a.tryQuickConnectMethod(method.method, url, method.body)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("all Quick Connect initiation methods failed, last error: %w", lastErr)
}

// tryQuickConnectMethod attempts Quick Connect initiation with a specific HTTP method
func (a *AuthAPI) tryQuickConnectMethod(method, url string, body []byte) (*QuickConnectData, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewBuffer(body)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", a.client.config.ClientName, a.client.config.Version))
	req.Header.Set("Authorization", a.client.GetAuthHeader())

	resp, err := a.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, string(responseBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	code, _ := result["Code"].(string)
	secret, _ := result["Secret"].(string)

	if code == "" || secret == "" {
		return nil, fmt.Errorf("invalid response: missing code or secret")
	}

	return &QuickConnectData{Code: code, Secret: secret}, nil
}

// CheckQuickConnectStatus checks the status of a Quick Connect session
func (a *AuthAPI) CheckQuickConnectStatus(secret string) (bool, error) {
	url := fmt.Sprintf("%s/QuickConnect/Connect?secret=%s", a.client.config.ServerURL, secret)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", a.client.config.ClientName, a.client.config.Version))
	req.Header.Set("Authorization", a.client.GetAuthHeader())

	resp, err := a.client.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	var quickConnectResponse QuickConnectStatus
	if err := json.Unmarshal(body, &quickConnectResponse); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	return quickConnectResponse.Authenticated, nil
}

// CompleteQuickConnect completes the Quick Connect authentication process
func (a *AuthAPI) CompleteQuickConnect(secret string) (string, string, error) {
	url := fmt.Sprintf("%s/Users/AuthenticateWithQuickConnect", a.client.config.ServerURL)

	requestBody := map[string]string{
		"Secret": secret,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", a.client.config.ClientName, a.client.config.Version))
	req.Header.Set("Authorization", a.client.GetAuthHeader())

	resp, err := a.client.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("failed to get Quick Connect result with status: %d, response: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read response: %w", err)
	}

	var result AuthenticationResult
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("failed to parse response: %w", err)
	}

	if result.AccessToken == "" {
		return "", "", fmt.Errorf("no access token in response")
	}

	if result.User.ID == "" {
		return "", "", fmt.Errorf("no user ID in response")
	}

	return result.AccessToken, result.User.ID, nil
}

// AuthenticateWithQuickConnect performs the complete Quick Connect authentication flow
func (a *AuthAPI) AuthenticateWithQuickConnect() error {
	enabled, err := a.CheckQuickConnectEnabled()
	if err != nil {
		return fmt.Errorf("failed to check Quick Connect status: %w", err)
	}

	if !enabled {
		return fmt.Errorf("Quick Connect is not enabled on this server, enable it in Dashboard > General > Quick Connect")
	}

	quickConnectData, err := a.InitiateQuickConnect()
	if err != nil {
		return fmt.Errorf("Quick Connect initiation failed: %w", err)
	}

	fmt.Printf("\nPlease enter this code in your Jellyfin app:\n\n    CODE: %s\n\nWaiting for approval (60 second timeout)...\n", quickConnectData.Code)

	timeout := time.Now().Add(60 * time.Second)
	for time.Now().Before(timeout) {
		authenticated, err := a.CheckQuickConnectStatus(quickConnectData.Secret)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		if authenticated {
			accessToken, userID, err := a.CompleteQuickConnect(quickConnectData.Secret)
			if err != nil {
				return fmt.Errorf("failed to complete Quick Connect: %w", err)
			}

			a.client.config.AccessToken = accessToken
			a.client.config.UserID = userID

			return nil
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("Quick Connect authentication timed out after 60 seconds")
}

// ValidateSession validates the current authentication session
func (a *AuthAPI) ValidateSession() error {
	if a.client.config.AccessToken == "" {
		return fmt.Errorf("no access token available")
	}

	url := fmt.Sprintf("%s/Library/MediaFolders", a.client.config.ServerURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create validation request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", a.client.config.AccessToken))
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", a.client.config.ClientName, a.client.config.Version))

	resp, err := a.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("validation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("session expired")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("validation failed with HTTP %d", resp.StatusCode)
	}

	return nil
}

// LoadSession loads a saved authentication session from disk
func (a *AuthAPI) LoadSession() error {
	sessionFile := filepath.Join(xdg.CacheHome, a.client.config.ClientName, "session.txt")
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		return fmt.Errorf("no session file found")
	}

	content, err := os.ReadFile(sessionFile)
	if err != nil {
		return fmt.Errorf("failed to read session file: %w", err)
	}

	// Try to parse as JSON for new format
	var sessionData SessionData
	if err := json.Unmarshal(content, &sessionData); err == nil {
		a.client.config.AccessToken = sessionData.AccessToken
		a.client.config.UserID = sessionData.UserID
	} else {
		// Old format - just the token
		a.client.config.AccessToken = strings.TrimSpace(string(content))
		// Need to get userID by validating
		if err := a.validateAndUpdateSession(); err != nil {
			return fmt.Errorf("failed to validate session and get userID: %w", err)
		}
	}

	return nil
}

// SaveSession saves the current authentication session to disk
func (a *AuthAPI) SaveSession() error {
	if a.client.config.AccessToken == "" || a.client.config.UserID == "" {
		return fmt.Errorf("no complete session data to save")
	}

	sessionDir := filepath.Join(xdg.CacheHome, a.client.config.ClientName)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	sessionData := SessionData{
		AccessToken: a.client.config.AccessToken,
		UserID:      a.client.config.UserID,
	}

	jsonData, err := json.Marshal(sessionData)
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	sessionFile := filepath.Join(sessionDir, "session.txt")
	return os.WriteFile(sessionFile, jsonData, 0o600)
}

// validateAndUpdateSession validates old sessions and gets the user ID
func (a *AuthAPI) validateAndUpdateSession() error {
	url := fmt.Sprintf("%s/Users/Me", a.client.config.ServerURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create user info request: %w", err)
	}

	req.Header.Set("X-Emby-Token", a.client.config.AccessToken)
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", a.client.config.ClientName, a.client.config.Version))

	resp, err := a.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("user info request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get user info: HTTP %d", resp.StatusCode)
	}

	var userInfo UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return fmt.Errorf("failed to decode user info: %w", err)
	}

	a.client.config.UserID = userInfo.ID
	return a.SaveSession()
}
