package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Banh-Canh/jtui/pkg/jellyfin"
)

// ViewType represents the current view state of the TUI.
type ViewType int

const (
	LibraryView ViewType = iota
	FolderView
	ItemView
	SearchView
)

// FilterType represents an item filter mode.
type FilterType int

const (
	FilterAll FilterType = iota
	FilterDownloaded
	FilterUnwatched
)

func (f FilterType) String() string {
	switch f {
	case FilterDownloaded:
		return "Downloaded"
	case FilterUnwatched:
		return "Unwatched"
	default:
		return ""
	}
}

// Path constants to avoid hardcoded strings scattered throughout the code.
const (
	mpvSocketPath     = "/tmp/jtui-mpvsocket"
	yaziCacheDir      = "/tmp/jtui_yazi_thumbs"
	oldThumbsCacheDir = "/tmp/jtui_thumbs"
)

// pathItem represents a breadcrumb entry in the navigation path.
type pathItem struct {
	name string
	id   string
}

// imageArea tracks the on-screen location of a rendered terminal image so it
// can be cleared before the next render.
type imageArea struct {
	x      int
	y      int
	width  int
	height int
	itemID string
}

// model is the Bubble Tea model that holds all TUI state.
type model struct {
	client         *jellyfin.Client
	currentView    ViewType
	items          []jellyfin.Item
	allItems       []jellyfin.Item // unfiltered items from server
	cursor         int
	currentPath    []pathItem
	currentDetails *jellyfin.DetailedItem
	loading        bool
	err            error
	successMsg     string
	searchQuery    string
	width          int
	height         int
	viewport       int
	viewportOffset int
	thumbnailCache map[string]string // Cache for rendered thumbnails
	// Video playback status
	currentPlayingItem  *jellyfin.DetailedItem
	currentPlayPosition float64 // Current position in seconds
	currentPlayDuration float64 // Total duration in seconds
	isVideoPlaying      bool
	// Cached track info (updated with progress tick, not in View())
	cachedSubtitleTrack string
	cachedAudioTrack    string
	// Cached download status (updated when details change, not every render)
	cachedDownloaded    bool
	cachedDownloadSize  int64
	cachedDownloadDirty bool // true when details changed and cache needs refresh
	// Download queue status
	dlQueueStatus jellyfin.QueueStatus
	// Item filter
	filter              FilterType
	downloadedIDCache   map[string]bool // item IDs known to be downloaded (sidecar + video exists)
	downloadedParentIDs map[string]bool // folder IDs/names that contain downloaded items
	downloadedFilenames map[string]bool // base filenames of downloaded .mkv files (lowercased)
	// Debounce & staleness tracking for detail loading
	detailSeq       uint64 // monotonic counter; incremented on every cursor move
	pendingDetailID string // item ID waiting to be loaded after debounce
	// Download status cache for item list rendering (avoids os.Stat in View)
	itemDownloadCache map[string]bool // itemID -> downloaded?  built lazily
}

// --- Messages ---------------------------------------------------------------

type librariesLoadedMsg struct {
	items []jellyfin.Item
}

type foldersLoadedMsg struct {
	items []jellyfin.Item
}

type itemsLoadedMsg struct {
	items []jellyfin.Item
}

type itemDetailsLoadedMsg struct {
	details *jellyfin.DetailedItem
	seq     uint64 // sequence number to detect stale responses
}

// detailDebounceMsg fires after a short delay to trigger the actual detail load.
type detailDebounceMsg struct {
	seq    uint64 // must match model.detailSeq to be valid
	itemID string
}

type searchResultsMsg struct {
	items []jellyfin.Item
}

type errMsg struct {
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

type successMsg struct {
	message string
}

type watchStatusUpdatedMsg struct {
	itemID  string
	watched bool
}

type thumbnailLoadedMsg struct {
	itemID    string
	cacheKey  string
	thumbnail string
}

type playbackProgressMsg struct {
	position      float64
	duration      float64
	isPlaying     bool
	subtitleTrack string
	audioTrack    string
}

type playbackStoppedMsg struct{}

type videoCompletedMsg struct {
	itemID string
}

type stopPlaybackMsg struct{}

type togglePauseMsg struct{}

type cycleSubtitleMsg struct{}

type cycleAudioMsg struct{}

type clearImageMsg struct{}

type downloadQueueUpdateMsg struct {
	status jellyfin.QueueStatus
}

// --- Styles -----------------------------------------------------------------

var (
	// Modern header styles
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E8E3F3")).
			Background(lipgloss.Color("#1a1b26")).
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#3b4261")).
			Padding(0, 2).
			Margin(0).
			Bold(true)

	headerTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#bb9af7")).
				Bold(true)

	headerStatusStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9ece6a"))

	headerOfflineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f7768e")).
				Bold(true)

	headerDividerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#3b4261"))

	// Panel title styles
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#bb9af7")).
			Background(lipgloss.Color("#1f2335")).
			Padding(0, 1).
			Bold(true)

	itemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#c0caf5"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1a1b26")).
			Background(lipgloss.Color("#bb9af7")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#565f89"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#c0caf5"))
)
