package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/polarfoxDev/marina/internal/logging"
)

// validateFileSize checks that at least one file with content (size > 0) exists in the given paths.
// It recursively walks directories and validates that there's at least one non-empty file.
// Returns an error if all files are empty or no files exist (indicating a likely backup failure).
func validateFileSize(paths []string, jobLogger *logging.JobLogger) error {
	totalFiles := 0
	emptyFiles := []string{}
	foundNonEmpty := false

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}

		if info.IsDir() {
			// Walk directory to check files
			err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				totalFiles++
				finfo, err := d.Info()
				if err != nil {
					return fmt.Errorf("get file info for %s: %w", p, err)
				}
				if finfo.Size() == 0 {
					emptyFiles = append(emptyFiles, p)
				} else {
					// Found a non-empty file, we can stop checking
					foundNonEmpty = true
					return filepath.SkipAll
				}
				return nil
			})
			// filepath.SkipAll is returned when we found a non-empty file (success case)
			if err != nil && !errors.Is(err, filepath.SkipAll) {
				return fmt.Errorf("error walking directory %s: %w", path, err)
			}
			// If we found a non-empty file, we're done
			if foundNonEmpty {
				return nil
			}
		} else {
			// Single file - check it
			totalFiles++
			if info.Size() == 0 {
				emptyFiles = append(emptyFiles, path)
			} else {
				// Found non-empty file, return success immediately
				return nil
			}
		}
	}

	// Check if we have any files at all
	if totalFiles == 0 {
		return fmt.Errorf("no files found in backup paths")
	}

	// If we got here, all files were empty
	jobLogger.Warn("all %d file(s) are empty (0 bytes)", totalFiles)
	return fmt.Errorf("all %d file(s) are empty (0 bytes) - backup likely failed silently", totalFiles)
}

// deduplicate removes duplicate strings from a slice
func deduplicate(slice []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
