package entity

import (
	"fmt"
	"path"
	"strings"

	"github.com/gosimple/slug"
	"github.com/streambinder/spotitube/util"
)

type Artwork struct {
	URL  string
	Data []byte
}

type Track struct {
	ID          string
	Title       string
	Artists     []string
	Album       string
	Artwork     Artwork
	Duration    int // in seconds
	Lyrics      string
	Number      int // track number within the album
	Year        int
	UpstreamURL string // URL to the upstream blob the song's been downloaded from
}

type TrackPath struct {
	track *Track
}

const (
	TrackFormat   = "mp3"
	ArtworkFormat = "jpg"
	LyricsFormat  = "txt"
)

// certain track titles include the variant description,
// this functions aims to strip out that part:
// > Title: Name - Acoustic
// > Song:  Name
func (track *Track) Song() (song string) {
	// it can very easily happen to encounter tracks
	// that contains artifacts in the title which do not
	// really define them as songs, rather indicate
	// the variant of the song
	song = track.Title
	song = strings.Split(song+" - ", " - ")[0]
	song = strings.Split(song+" (", " (")[0]
	song = strings.Split(song+" [", " [")[0]
	return
}

func (track *Track) Path() TrackPath {
	return TrackPath{track}
}

func (trackPath TrackPath) Final() string {
	// Get the primary artist and remove dots
	primaryArtist := trackPath.track.Artists[0]
	primaryArtist = strings.ReplaceAll(primaryArtist, ".", "")

	// Format the title with remix information in parentheses
	title := trackPath.track.Title

	// Check if title contains remix/version information after a dash
	if idx := strings.Index(title, " - "); idx > 0 {
		baseName := strings.TrimSpace(title[:idx])
		remixInfo := strings.TrimSpace(title[idx+3:])
		title = fmt.Sprintf("%s (%s)", baseName, remixInfo)
	}

	// Add featured artists if there are any (more than the primary artist)
	if len(trackPath.track.Artists) > 1 {
		// Get all the featured artists (excluding the primary artist)
		featuredArtists := make([]string, 0, len(trackPath.track.Artists)-1)
		for i := 1; i < len(trackPath.track.Artists); i++ {
			// Remove dots from featured artists too
			artist := strings.ReplaceAll(trackPath.track.Artists[i], ".", "")
			featuredArtists = append(featuredArtists, artist)
		}

		// Add featuring artists to the title
		title = fmt.Sprintf("%s (ft %s)", title, strings.Join(featuredArtists, ", "))
	}

	// Generate the final path: "Artist - Title (Remix Info) (ft FeaturedArtist1, FeaturedArtist2).mp3"
	return util.LegalizeFilename(fmt.Sprintf("%s - %s.%s", primaryArtist, title, TrackFormat))
}

func (trackPath TrackPath) Download() string {
	return util.CacheFile(
		util.LegalizeFilename(fmt.Sprintf("%s.%s", slug.Make(trackPath.track.ID), TrackFormat)),
	)
}

func (trackPath TrackPath) Artwork() string {
	return util.CacheFile(
		util.LegalizeFilename(fmt.Sprintf("%s.%s", slug.Make(path.Base(trackPath.track.Artwork.URL)), ArtworkFormat)),
	)
}

func (trackPath TrackPath) Lyrics() string {
	return util.CacheFile(
		util.LegalizeFilename(fmt.Sprintf("%s.%s", slug.Make(trackPath.track.ID), LyricsFormat)),
	)
}
