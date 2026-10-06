package app

// The central UI palette (plan UI_revamp, step UI-1). Every color the UI
// uses is defined here as an AdaptiveColor with dark/light terminal
// variants; view.go, delegates.go and confirm.go only reference these
// styles, so a palette change never touches the layout code.

import "github.com/charmbracelet/lipgloss"

var (
	colOK       = lipgloss.AdaptiveColor{Light: "#008800", Dark: "#00BB00"} // OK, archived files
	colWork     = lipgloss.AdaptiveColor{Light: "#B26800", Dark: "#FFBB00"} // processing, in progress
	colDamaged  = lipgloss.AdaptiveColor{Light: "#AA0000", Dark: "#BB0000"} // damaged, errors
	colDir      = lipgloss.AdaptiveColor{Light: "#0000EE", Dark: "#0044FF"} // directories
	colTicked   = lipgloss.AdaptiveColor{Light: "#887700", Dark: "#BBAA00"} // files marked for creation
	colDim      = lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"} // meta, labels, hints
	colCursor   = lipgloss.AdaptiveColor{Light: "#0066CC", Dark: "#0044FF"} // the cursor bar — the only filled background in the UI
	colCursorTx = lipgloss.Color("15")                                      // text on the cursor bar
)

var (
	styleOK      = lipgloss.NewStyle().Foreground(colOK)
	styleWork    = lipgloss.NewStyle().Foreground(colWork)
	styleDamaged = lipgloss.NewStyle().Foreground(colDamaged)
	styleDir     = lipgloss.NewStyle().Foreground(colDir).Bold(true)
	styleTicked  = lipgloss.NewStyle().Foreground(colTicked)
	styleDim     = lipgloss.NewStyle().Foreground(colDim)
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleCursor  = lipgloss.NewStyle().Background(colCursor).Foreground(colCursorTx)
)
