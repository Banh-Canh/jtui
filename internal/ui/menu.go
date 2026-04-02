package ui

import (
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Banh-Canh/jtui/pkg/jellyfin"
)

// Global program reference to send messages from background goroutines.
var globalProgram *tea.Program

// globalImageArea tracks the current Kitty image position for cleanup.
var globalImageArea *imageArea

// Menu launches the TUI with no pre-authenticated client.
func Menu() {
	setupCleanupHandlers()
	go cleanupYaziCache()

	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	globalProgram = p
	if _, err := p.Run(); err != nil {
		CleanupMpvProcesses()
		os.Exit(1)
	}
	CleanupMpvProcesses()
}

// MenuWithClient launches the TUI with a pre-authenticated client.
func MenuWithClient(client *jellyfin.Client) {
	setupCleanupHandlers()
	go cleanupYaziCache()

	p := tea.NewProgram(initialModelWithClient(client), tea.WithAltScreen(), tea.WithMouseCellMotion())
	globalProgram = p
	if _, err := p.Run(); err != nil {
		CleanupMpvProcesses()
		os.Exit(1)
	}
	CleanupMpvProcesses()
}

// setupCleanupHandlers sets up signal handlers to cleanup mpv processes on exit.
func setupCleanupHandlers() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		CleanupMpvProcesses()
		os.Exit(0)
	}()
}
