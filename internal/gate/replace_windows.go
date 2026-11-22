package gate

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransient names the errors cmd/go's robustio retries a rename on: another process, such as a virus scanner or
// another server reading the marker, has one of the files open.
func isTransient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_FILE_NOT_FOUND)
}
