package widget

// The script host's areas, each registered from its own init() in its own file
// (internal/widget/script, RegisterHostArea). Importing their packages links every area into the
// service; adding an area is adding a file to one of them.
import (
	_ "github.com/NaejEL/opnview/internal/widget/hostareas/readers"
	_ "github.com/NaejEL/opnview/internal/widget/hostareas/values"
)
