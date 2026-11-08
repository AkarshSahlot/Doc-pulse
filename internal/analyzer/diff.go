package analyzer

import (
	"path/filepath"
	"strings"
)

// ChangedFile represents a file that was modified in a diff.
type ChangedFile struct {
	Filename  string
	Extension string
	Patch     string // The raw diff/patch content, when available
}

// ParseChangedFiles extracts file information from a list of filenames.
func ParseChangedFiles(filenames []string) []ChangedFile {
	files := make([]ChangedFile, 0, len(filenames))
	for _, f := range filenames {
		files = append(files, ChangedFile{
			Filename:  f,
			Extension: strings.ToLower(filepath.Ext(f)),
		})
	}
	return files
}

var codeExtensions = map[string]bool{
	".go":    true,
	".py":    true,
	".js":    true,
	".ts":    true,
	".java":  true,
	".rs":    true,
	".rb":    true,
	".c":     true,
	".cpp":   true,
	".h":     true,
	".hpp":   true,
	".cs":    true,
	".php":   true,
	".swift": true,
	".kt":    true,
}

// IsCodeFile returns true if the extension belongs to a source file that could affect documentation.
func IsCodeFile(ext string) bool {
	return codeExtensions[ext]
}

// FilterCodeFiles returns only the code files from a list of changed files.
func FilterCodeFiles(files []ChangedFile) []ChangedFile {
	var result []ChangedFile
	for _, f := range files {
		if IsCodeFile(f.Extension) {
			result = append(result, f)
		}
	}
	return result
}
