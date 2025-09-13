package jellyfin

import (
	"fmt"
	"strings"
)

// Item represents a Jellyfin media item interface
type Item interface {
	GetName() string
	GetID() string
	GetIsFolder() bool
}

// SimpleItem is a basic implementation of Item
type SimpleItem struct {
	Name     string `json:"Name"`
	ID       string `json:"Id"`
	IsFolder bool   `json:"IsFolder"`
	Type     string `json:"Type,omitempty"`
}

func (s SimpleItem) GetName() string {
	return s.Name
}

func (s SimpleItem) GetID() string {
	return s.ID
}

func (s SimpleItem) GetIsFolder() bool {
	return s.IsFolder
}

// DetailedItem represents a Jellyfin item with additional metadata
type DetailedItem struct {
	SimpleItem
	Overview       string   `json:"Overview"`
	ProductionYear int      `json:"ProductionYear"`
	RunTimeTicks   int64    `json:"RunTimeTicks"`
	Genres         []string `json:"Genres"`
	Studios        []struct {
		Name string `json:"Name"`
	} `json:"Studios"`
	ImageTags struct {
		Primary string `json:"Primary"`
	} `json:"ImageTags"`
	BackdropImageTags []string `json:"BackdropImageTags"`
	UserData          struct {
		PlaybackPositionTicks int64   `json:"PlaybackPositionTicks"`
		PlayCount             int     `json:"PlayCount"`
		IsFavorite            bool    `json:"IsFavorite"`
		Played                bool    `json:"Played"`
		PlayedPercentage      float64 `json:"PlayedPercentage"`
	} `json:"UserData"`

	// Series/Season information
	SeriesName        string `json:"SeriesName,omitempty"`
	SeasonName        string `json:"SeasonName,omitempty"`
	ParentIndexNumber int    `json:"ParentIndexNumber,omitempty"`
	IndexNumber       int    `json:"IndexNumber,omitempty"`
}

// Additional methods for DetailedItem
func (d DetailedItem) GetOverview() string {
	return d.Overview
}

func (d DetailedItem) GetYear() int {
	return d.ProductionYear
}

func (d DetailedItem) GetRuntime() string {
	if d.RunTimeTicks == 0 {
		return ""
	}
	minutes := d.RunTimeTicks / (10000000 * 60) // Convert from ticks to minutes
	hours := minutes / 60
	mins := minutes % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func (d DetailedItem) GetGenres() string {
	if len(d.Genres) == 0 {
		return ""
	}
	return strings.Join(d.Genres, ", ")
}

func (d DetailedItem) GetStudio() string {
	if len(d.Studios) == 0 {
		return ""
	}
	return d.Studios[0].Name
}

func (d DetailedItem) HasPrimaryImage() bool {
	return d.ImageTags.Primary != ""
}

func (d DetailedItem) GetPlaybackPositionTicks() int64 {
	return d.UserData.PlaybackPositionTicks
}

func (d DetailedItem) IsWatched() bool {
	return d.UserData.Played
}

func (d DetailedItem) GetPlayedPercentage() float64 {
	return d.UserData.PlayedPercentage
}

func (d DetailedItem) HasResumePosition() bool {
	return d.UserData.PlaybackPositionTicks > 0 && !d.UserData.Played
}

func (d DetailedItem) GetSeriesName() string {
	return d.SeriesName
}

func (d DetailedItem) GetSeasonName() string {
	return d.SeasonName
}

func (d DetailedItem) GetSeasonNumber() int {
	return d.ParentIndexNumber
}

func (d DetailedItem) GetEpisodeNumber() int {
	return d.IndexNumber
}

// QuickConnectData holds Quick Connect authentication data
type QuickConnectData struct {
	Code     string `json:"code"`
	Secret   string `json:"secret"`
	DeviceID string `json:"DeviceId"`
}

// SessionData holds session information for persistence
type SessionData struct {
	AccessToken string `json:"access_token"`
	UserID      string `json:"user_id"`
}

// ItemsResponse represents the response from Items API endpoints
type ItemsResponse struct {
	Items []SimpleItem `json:"Items"`
}

// DetailedItemsResponse represents the response from detailed Items API endpoints
type DetailedItemsResponse struct {
	Items []DetailedItem `json:"Items"`
}

// PlaybackInfo holds playback session information
type PlaybackInfo struct {
	ItemID        string `json:"ItemId"`
	SessionID     string `json:"SessionId"`
	MediaSourceID string `json:"MediaSourceId"`
	PositionTicks int64  `json:"PositionTicks,omitempty"`
	CanSeek       bool   `json:"CanSeek,omitempty"`
	PlayMethod    string `json:"PlayMethod,omitempty"`
}

// UserInfo represents user information
type UserInfo struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

// QuickConnectStatus represents the status of a Quick Connect session
type QuickConnectStatus struct {
	Authenticated bool `json:"Authenticated"`
}

// AuthenticationResult represents the result of authentication
type AuthenticationResult struct {
	AccessToken string   `json:"AccessToken"`
	User        UserInfo `json:"User"`
}
