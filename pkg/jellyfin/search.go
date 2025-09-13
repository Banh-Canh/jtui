package jellyfin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// SearchAPI handles search-related operations
type SearchAPI struct {
	client *Client
}

// SearchOptions represents search configuration options
type SearchOptions struct {
	Query     string
	Limit     int
	Recursive bool
	Fields    []string // Specific fields to return for better performance
}

// NewSearchOptions creates default search options
func NewSearchOptions(query string) *SearchOptions {
	return &SearchOptions{
		Query:     query,
		Limit:     50,
		Recursive: true,
		Fields:    []string{"BasicSyncInfo", "PrimaryImageAspectRatio"}, // Minimal fields for performance
	}
}

// WithLimit sets the maximum number of results to return
func (s *SearchOptions) WithLimit(limit int) *SearchOptions {
	s.Limit = limit
	return s
}

// WithRecursive sets whether to search recursively through subdirectories
func (s *SearchOptions) WithRecursive(recursive bool) *SearchOptions {
	s.Recursive = recursive
	return s
}

// Items searches for items using the Jellyfin search API
func (s *SearchAPI) Items(options *SearchOptions) ([]Item, error) {
	if !s.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	if options == nil {
		return nil, fmt.Errorf("search options cannot be nil")
	}

	if options.Query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	searchURL := fmt.Sprintf(
		"%s/Users/%s/Items?searchTerm=%s&Recursive=%t&Fields=BasicSyncInfo,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1&Limit=%d",
		s.client.config.ServerURL,
		s.client.config.UserID,
		url.QueryEscape(options.Query),
		options.Recursive,
		options.Limit,
	)

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		s.client.config.ClientName,
		s.client.config.ClientName,
		s.client.config.DeviceID,
		s.client.config.Version,
		s.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var response ItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	items := make([]Item, len(response.Items))
	for i, item := range response.Items {
		items[i] = item
	}

	return items, nil
}

// Quick performs a quick search with default options
func (s *SearchAPI) Quick(query string) ([]Item, error) {
	return s.Items(NewSearchOptions(query))
}
