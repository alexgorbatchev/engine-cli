package playlist

import (
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const (
	m3uUTF8        = "utf-8"
	m3uWindows1252 = "windows-1252"
	m3uLatin1      = "iso-8859-1"
)

func playlistFileError(code, message string) error {
	return &progress.Error{Code: code, Cause: errors.New(message)}
}
