package ipc

import (
	"fmt"
	"strings"
)

// resolveMountPath splits a "mount:subpath" path into its mount name and
// subpath. Paths without a colon are assumed to be on the "local" mount.
func resolveMountPath(fullPath string) (mount string, subPath string) {
	parts := strings.SplitN(fullPath, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "local", fullPath
}

func errMountNotFound(name string) error {
	return fmt.Errorf("mount not found: %s", name)
}

func errTaskNotFound(id string) error {
	return fmt.Errorf("task not found: %s", id)
}

var errStreamServerNotInitialized = fmt.Errorf("stream server not initialized")

var errTelegramNotInitialized = fmt.Errorf("telegram manager not initialized")
