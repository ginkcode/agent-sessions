package files

// TerminalFile identifies an existing regular file within an authorized catalog
// directory. Path is slash-separated and relative to Dir, as ReadFile expects.
type TerminalFile struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
}
