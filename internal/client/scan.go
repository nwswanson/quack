package client

import (
	"fmt"
	"os"
)

func validateDirectory(directory string) error {
	if directory == "" {
		return fmt.Errorf("directory is required")
	}

	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("directory is not a directory: %s", directory)
	}
	return nil
}
