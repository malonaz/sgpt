package keymap

// Bindings are global: a key means one thing wherever it is pressed. The
// bindings below are the actions more than one screen performs — moving
// through a list, deleting whatever is selected — declared once here and
// held by every screen that performs them, so rebinding one moves the key
// everywhere it acts.
//
// Every other binding is declared by the package that owns it.
var (
	KeyUp             = New("up", "Move up", "ctrl+p")
	KeyDown           = New("down", "Move down", "ctrl+n")
	KeyToTop          = New("to_top", "Jump to top", "alt+<")
	KeyToBottom       = New("to_bottom", "Jump to bottom", "alt+>")
	KeyDelete         = New("delete", "Delete selected chat / message", "alt+d")
	KeyToggleFavorite = New("toggle_favorite", "Toggle favorite", "alt+shift+f")
	KeyOpenInEditor   = New("open_in_editor", "Open selection / compose in $EDITOR", "alt+o")
)
