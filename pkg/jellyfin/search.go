package jellyfin

import (
	"fmt"
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
	Fields    []string
}

// NewSearchOptions creates default search options
func NewSearchOptions(query string) *SearchOptions {
	return &SearchOptions{
		Query:     query,
		Limit:     50,
		Recursive: true,
		Fields:    []string{"BasicSyncInfo", "PrimaryImageAspectRatio"},
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

	var response ItemsResponse
	if err := s.client.doRequestDecode("GET", searchURL, nil, &response); err != nil {
		return nil, err
	}

	return toItems(response.Items), nil
}

// Quick performs a quick search with default options
func (s *SearchAPI) Quick(query string) ([]Item, error) {
	return s.Items(NewSearchOptions(query))
}
