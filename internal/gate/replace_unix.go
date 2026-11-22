//go:build !windows

package gate

// isTransient is always false: here a rename replaces a file other processes have open.
func isTransient(error) bool {
	return false
}
