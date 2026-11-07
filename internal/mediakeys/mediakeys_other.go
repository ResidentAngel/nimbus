//go:build !windows

package mediakeys

// Listen на macOS и Linux пока не реализован: там нужны MPRemoteCommandCenter
// (macOS) или MPRIS через D-Bus (Linux).
func Listen() (*Listener, error) { return nil, ErrUnsupported }
