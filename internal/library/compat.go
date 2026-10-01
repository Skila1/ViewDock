package library

import (
	"path/filepath"
	"strings"
)

// Item kinds a library can hold. Every catalogued title keeps its own kind
// (movie, or series with its seasons and episodes) whatever library it is in,
// so a Mixed library can later be split into Movies and TV Shows libraries.
const (
	KindMovie  = "movie"
	KindSeries = "series"
)

// Accepts reports whether a library of contentType may hold a title of
// itemKind. Movies libraries hold movies, TV libraries hold shows, and Mixed
// libraries hold both. Episodes and seasons belong to their show.
func Accepts(contentType, itemKind string) bool {
	switch itemKind {
	case KindMovie:
		return contentType == "movies" || contentType == "mixed"
	case KindSeries, "season", "episode":
		return contentType == "tv" || contentType == "mixed"
	}
	return false
}

// ContentTypeLabel is the name the web app shows for a content type.
func ContentTypeLabel(contentType string) string {
	switch contentType {
	case "movies":
		return "Movies"
	case "tv":
		return "TV Shows"
	case "mixed":
		return "Mixed"
	}
	return contentType
}

// VideoExtensions are the file extensions catalogued as video. The scanner
// and the mover share this list so a move never leaves a video behind that
// a scan would have picked up.
var VideoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	".mov": true, ".ts": true, ".m2ts": true, ".wmv": true,
	".webm": true, ".mpg": true, ".mpeg": true, ".flv": true,
}

// IsVideoName reports whether name has a video extension.
func IsVideoName(name string) bool {
	return VideoExtensions[strings.ToLower(filepath.Ext(name))]
}
