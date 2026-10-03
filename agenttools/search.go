package agenttools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// SearchMatch is one bounded, line-oriented repository search result.
type SearchMatch struct {
	Path          string   `json:"path"`
	Line          int      `json:"line"`
	Text          string   `json:"text"`
	After         []string `json:"after,omitempty"`
	TextTruncated bool     `json:"text_truncated,omitempty"`
}

type contentSearchQuery struct {
	Path         string
	FilenameGlob string
	ExcludeGlob  string
	Regex        string
	IgnoreCase   bool
	ResultMode   string
	AfterLines   int
	MaxResults   int
}

type contentSearchResult struct {
	Matches      []SearchMatch
	Files        []string
	MatchCount   *int
	Truncated    bool
	SkippedFiles int
}

var errSearchLimit = errors.New("agent tool result limit reached")

func searchDirectory(root, relative string) (string, error) {
	directory, err := existingPath(root, relative)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return "", ErrInvalidCall
	}
	return directory, nil
}

func validateFilenameGlob(pattern string) error {
	if pattern == "" || len(pattern) > 256 || path.IsAbs(pattern) {
		return ErrInvalidCall
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ErrInvalidCall
		}
		if segment != "**" {
			if _, err := path.Match(segment, "x"); err != nil {
				return ErrInvalidCall
			}
		}
	}
	return nil
}

func filenameGlobMatches(pattern, relative string) bool {
	if pattern == "" {
		return true
	}
	if !strings.Contains(pattern, "/") {
		matched, _ := path.Match(pattern, path.Base(relative))
		return matched
	}
	segments := strings.Split(pattern, "/")
	parts := strings.Split(filepath.ToSlash(relative), "/")
	var match func(int, int) bool
	match = func(i, j int) bool {
		if i == len(segments) {
			return j == len(parts)
		}
		if segments[i] == "**" {
			return match(i+1, j) || j < len(parts) && match(i, j+1)
		}
		if j == len(parts) {
			return false
		}
		matched, _ := path.Match(segments[i], parts[j])
		return matched && match(i+1, j+1)
	}
	return match(0, 0)
}

func walkWorkspaceFiles(ctx context.Context, root, directory string, visit func(string, string, os.DirEntry) error) error {
	count := 0
	return filepath.WalkDir(directory, func(absolute string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// WalkDir never follows symlinked directories. Do not report or read
		// symlinked files either, including links to outside the workspace.
		if !entry.Type().IsRegular() {
			return nil
		}
		count++
		if count > 50000 {
			return ErrTooLarge
		}
		relative, err := filepath.Rel(root, absolute)
		if err != nil || !withinRoot(root, absolute) {
			return ErrBoundary
		}
		return visit(absolute, filepath.ToSlash(relative), entry)
	})
}

func findFiles(ctx context.Context, root, relativeDirectory, pattern, startAfter string, limit int) ([]string, bool, string, error) {
	if err := validateFilenameGlob(pattern); err != nil {
		return nil, false, "", err
	}
	directory, err := searchDirectory(root, relativeDirectory)
	if err != nil {
		return nil, false, "", err
	}
	files := make([]string, 0)
	err = walkWorkspaceFiles(ctx, root, directory, func(_ string, relative string, _ os.DirEntry) error {
		if filenameGlobMatches(pattern, relative) {
			files = append(files, relative)
		}
		return nil
	})
	if err != nil {
		return nil, false, "", err
	}
	sort.Strings(files)
	start := sort.SearchStrings(files, startAfter)
	for start < len(files) && files[start] <= startAfter {
		start++
	}
	files = files[start:]
	if len(files) > limit {
		return files[:limit], true, files[limit-1], nil
	}
	return files, false, "", nil
}

func searchFileContents(ctx context.Context, root string, query contentSearchQuery) (contentSearchResult, error) {
	if query.Regex == "" || len(query.Regex) > 4096 || query.MaxResults < 1 || query.MaxResults > 100 || query.AfterLines < 0 || query.AfterLines > 40 {
		return contentSearchResult{}, ErrInvalidCall
	}
	if query.ResultMode == "" {
		query.ResultMode = "lines"
	}
	if query.ResultMode != "lines" && query.ResultMode != "filenames" && query.ResultMode != "count" || query.AfterLines > 0 && query.ResultMode != "lines" {
		return contentSearchResult{}, ErrInvalidCall
	}
	for _, pattern := range []string{query.FilenameGlob, query.ExcludeGlob} {
		if pattern != "" {
			if err := validateFilenameGlob(pattern); err != nil {
				return contentSearchResult{}, err
			}
		}
	}
	expression := query.Regex
	if query.IgnoreCase {
		expression = "(?i:" + expression + ")"
	}
	re, err := regexp.Compile(expression)
	if err != nil {
		return contentSearchResult{}, ErrInvalidCall
	}
	target, err := existingPath(root, query.Path)
	if err != nil {
		return contentSearchResult{}, err
	}
	targetInfo, err := os.Stat(target)
	if err != nil || !targetInfo.IsDir() && !targetInfo.Mode().IsRegular() {
		return contentSearchResult{}, ErrInvalidCall
	}
	result := contentSearchResult{}
	if query.ResultMode == "count" {
		count := 0
		result.MatchCount = &count
	}
	scannedBytes, outputBytes := int64(0), 0
	visit := func(absolute, relative string) error {
		if !filenameGlobMatches(query.FilenameGlob, relative) || query.ExcludeGlob != "" && filenameGlobMatches(query.ExcludeGlob, relative) {
			return nil
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return err
		}
		// Return an explicitly incomplete result for oversized or binary files,
		// instead of silently treating them as evidence of absence.
		if info.Size() > 2<<20 {
			result.SkippedFiles++
			return nil
		}
		scannedBytes += info.Size()
		if scannedBytes > 64<<20 {
			return ErrTooLarge
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			return err
		}
		if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			result.SkippedFiles++
			return nil
		}
		if len(content) == 0 {
			return nil
		}
		lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
		for index, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			switch query.ResultMode {
			case "count":
				*result.MatchCount = *result.MatchCount + 1
			case "filenames":
				if len(result.Files) == query.MaxResults {
					result.Truncated = true
					return errSearchLimit
				}
				result.Files = append(result.Files, relative)
				return nil
			case "lines":
				text, clipped := boundedSearchLine(line, 2048)
				match := SearchMatch{Path: relative, Line: index + 1, Text: text, TextTruncated: clipped}
				size := len(relative) + len(text) + 32
				for next := index + 1; next < len(lines) && next <= index+query.AfterLines; next++ {
					following, _ := boundedSearchLine(lines[next], 2048)
					match.After = append(match.After, following)
					size += len(following) + 4
				}
				if len(result.Matches) == query.MaxResults || outputBytes+size > 64<<10 {
					result.Truncated = true
					return errSearchLimit
				}
				outputBytes += size
				result.Matches = append(result.Matches, match)
			}
		}
		return nil
	}
	if targetInfo.IsDir() {
		err = walkWorkspaceFiles(ctx, root, target, func(absolute, relative string, _ os.DirEntry) error {
			return visit(absolute, relative)
		})
	} else {
		if err = ctx.Err(); err == nil {
			var relative string
			relative, err = filepath.Rel(root, target)
			if err == nil {
				err = visit(target, filepath.ToSlash(relative))
			}
		}
	}
	if err != nil && !errors.Is(err, errSearchLimit) {
		return contentSearchResult{}, err
	}
	result.Truncated = result.Truncated || result.SkippedFiles > 0
	return result, nil
}

func boundedSearchLine(line string, limit int) (string, bool) {
	if len(line) <= limit {
		return line, false
	}
	cut := limit
	for !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut], true
}
