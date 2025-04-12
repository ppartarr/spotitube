package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/adrg/xdg"
	"github.com/arunsworld/nursery"
	"github.com/bogem/id3v2/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/streambinder/spotitube/downloader"
	"github.com/streambinder/spotitube/entity"
	"github.com/streambinder/spotitube/entity/id3"
	"github.com/streambinder/spotitube/entity/index"
	"github.com/streambinder/spotitube/entity/playlist"
	"github.com/streambinder/spotitube/lyrics"
	"github.com/streambinder/spotitube/processor"
	"github.com/streambinder/spotitube/provider"
	"github.com/streambinder/spotitube/spotify"
	"github.com/streambinder/spotitube/util"
	"github.com/streambinder/spotitube/util/anchor"
)

const (
	routineTypeIndex int = iota
	routineTypeAuth
	routineTypeDecide
	routineTypeCollect
	routineTypeProcess
	routineTypeInstall
	routineTypeMix
)

var (
	routineSemaphores map[int](chan bool)
	routineQueues     map[int](chan interface{})
	tui               = anchor.New(anchor.Red)
)

func init() {
	cmdRoot.AddCommand(cmdSync())
}

func cmdSync() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "sync",
		Short:        "Synchronize collections",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var (
				path             = util.ErrWrap(xdg.UserDirs.Music)(cmd.Flags().GetString("output"))
				playlistEncoding = util.ErrWrap("m3u")(cmd.Flags().GetString("playlist-encoding"))
				manual           = util.ErrWrap(false)(cmd.Flags().GetBool("manual"))
				library          = util.ErrWrap(false)(cmd.Flags().GetBool("library"))
				playlists        = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("playlist"))
				playlistsTracks  = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("playlist-tracks"))
				albums           = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("album"))
				tracks           = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("track"))
				fixes            = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("fix"))
				libraryLimit     = util.ErrWrap(0)(cmd.Flags().GetInt("library-limit"))
				lyrics           = util.ErrWrap(false)(cmd.Flags().GetBool("lyrics"))
			)

			for index, path := range fixes {
				if absPath, err := filepath.Abs(path); err == nil {
					fixes[index] = absPath
				}
			}

			if err := os.Chdir(path); err != nil {
				return err
			}

			if err := nursery.RunConcurrently(
				routineIndex(path),
				routineAuth,
				routineFetch(library, playlists, playlistsTracks, albums, tracks, fixes, libraryLimit),
				routineDecide(manual, path),
				routineCollect(lyrics),
				routineProcess,
				routineInstall,
				routineMix(playlistEncoding),
			); err != nil {
				return err
			}

			tui.Printf("synchronization complete")
			return nil
		},
		PreRun: func(cmd *cobra.Command, _ []string) {
			routineSemaphores = map[int](chan bool){
				routineTypeIndex:   make(chan bool, 1),
				routineTypeAuth:    make(chan bool, 1),
				routineTypeInstall: make(chan bool, 1),
			}
			routineQueues = map[int](chan interface{}){
				routineTypeDecide:  make(chan interface{}, 10000),
				routineTypeCollect: make(chan interface{}, 10000),
				routineTypeProcess: make(chan interface{}, 10000),
				routineTypeInstall: make(chan interface{}, 10),
				routineTypeMix:     make(chan interface{}, 10000),
			}

			var (
				playlists       = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("playlist"))
				playlistsTracks = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("playlist-tracks"))
				albums          = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("album"))
				tracks          = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("track"))
				fixes           = util.ErrWrap([]string{})(cmd.Flags().GetStringArray("fix"))
			)
			if len(playlists)+len(playlistsTracks)+len(albums)+len(tracks)+len(fixes) == 0 {
				cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
					if f.Name == "library" {
						util.ErrSuppress(f.Value.Set("true"))
					}
				})
			}
		},
	}
	cmd.Flags().StringP("output", "o", xdg.UserDirs.Music, "Output synchronization path")
	cmd.Flags().String("playlist-encoding", "m3u", "Playlist output files encoding")
	cmd.Flags().BoolP("manual", "m", false, "Enable manual mode (prompts for user-issued URL to use for download)")
	cmd.Flags().BoolP("library", "l", false, "Synchronize library (auto-enabled if no collection is supplied)")
	cmd.Flags().StringArrayP("playlist", "p", []string{}, "Synchronize playlist")
	cmd.Flags().StringArray("playlist-tracks", []string{}, "Synchronize playlist tracks without playlist file")
	cmd.Flags().StringArrayP("album", "a", []string{}, "Synchronize album")
	cmd.Flags().StringArrayP("track", "t", []string{}, "Synchronize track")
	cmd.Flags().StringArrayP("fix", "f", []string{}, "Fix local track")
	cmd.Flags().Int("library-limit", 0, "Number of tracks to fetch from library (unlimited if 0)")
	cmd.Flags().BoolP("lyrics", "y", false, "Fetch lyrics from genius")
	return cmd
}

// fuzzyFindTracksForSpotify attempts to match local files without Spotify IDs to Spotify tracks
func fuzzyFindTracksForSpotify(localPath string, tracks []*entity.Track) error {
	// Map to store paths of files without Spotify IDs
	filesWithoutIDs := map[string]bool{}

	// First pass - collect all MP3 files without Spotify IDs
	err := filepath.Walk(localPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process MP3 files
		if !strings.HasSuffix(strings.ToLower(path), entity.TrackFormat) {
			return nil
		}

		// Check if file has Spotify ID
		tag, err := id3.Open(path, id3v2.Options{Parse: true})
		if err != nil {
			return nil // Skip files we can't open
		}
		defer tag.Close()

		// If no Spotify ID, add to our map
		if len(tag.SpotifyID()) == 0 {
			filesWithoutIDs[path] = true
			tui.Printf("Found file without Spotify ID: %s", filepath.Base(path))
		}

		return nil
	})

	if err != nil {
		return err
	}

	// No files without IDs found
	if len(filesWithoutIDs) == 0 {
		return nil
	}

	tui.Printf("Found %d files without Spotify IDs. Attempting to match with Spotify tracks...", len(filesWithoutIDs))

	// For each Spotify track, try to find a matching local file
	for _, track := range tracks {
		// Clean track data for matching
		cleanTerm := func(input string) string {
			result := strings.ToLower(input)
			result = strings.ReplaceAll(result, "&", "and")
			result = strings.ReplaceAll(result, "-", " ")
			result = strings.ReplaceAll(result, "_", " ")
			result = strings.ReplaceAll(result, "(", " ")
			result = strings.ReplaceAll(result, ")", " ")
			result = strings.ReplaceAll(result, "[", " ")
			result = strings.ReplaceAll(result, "]", " ")
			result = strings.ReplaceAll(result, "feat.", "")
			result = strings.ReplaceAll(result, "feat", "")
			result = strings.ReplaceAll(result, "ft.", "")
			result = strings.ReplaceAll(result, "ft", "")
			result = strings.ReplaceAll(result, "mix", "")
			result = strings.ReplaceAll(result, "remix", "")
			result = strings.ReplaceAll(result, "edit", "")
			result = strings.ReplaceAll(result, "version", "")
			result = strings.TrimSpace(result)
			return result
		}

		// Get track info
		title := cleanTerm(track.Title)
		artist := cleanTerm(track.Artists[0])

		// Get base title (without remix info)
		baseTitle := track.Title
		if idx := strings.Index(baseTitle, " -"); idx > 0 {
			baseTitle = strings.TrimSpace(baseTitle[:idx])
		}
		if idx := strings.Index(baseTitle, "("); idx > 0 {
			baseTitle = strings.TrimSpace(baseTitle[:idx])
		}
		baseTitle = cleanTerm(baseTitle)

		// Extract individual words for matching
		titleWords := strings.Fields(title)
		artistWords := strings.Fields(artist)

		// For each file without an ID, see if it matches this track
		for filePath := range filesWithoutIDs {
			fileName := strings.ToLower(filepath.Base(filePath))
			cleanFileName := cleanTerm(fileName)

			// Check for matches using different criteria
			isMatch := false

			// High priority match - exact artist and title
			if strings.Contains(cleanFileName, artist) && strings.Contains(cleanFileName, title) {
				isMatch = true
			} else if strings.Contains(cleanFileName, artist) && strings.Contains(cleanFileName, baseTitle) {
				// Medium priority - artist and base title (without remix info)
				isMatch = true
			} else {
				// Lower priority - look for combinations of words
				matchCount := 0
				// Check for title words
				for _, word := range titleWords {
					if len(word) > 3 && strings.Contains(cleanFileName, word) {
						matchCount++
					}
				}
				// Check for artist words
				for _, word := range artistWords {
					if len(word) > 3 && strings.Contains(cleanFileName, word) {
						matchCount++
					}
				}

				// If we match at least 2 words and include at least one word from both title and artist
				if matchCount >= 2 {
					titleWordFound := false
					artistWordFound := false

					for _, word := range titleWords {
						if len(word) > 3 && strings.Contains(cleanFileName, word) {
							titleWordFound = true
							break
						}
					}

					for _, word := range artistWords {
						if len(word) > 3 && strings.Contains(cleanFileName, word) {
							artistWordFound = true
							break
						}
					}

					if titleWordFound && artistWordFound {
						isMatch = true
					}
				}
			}

			// If we found a match, update the ID3 tags and index
			if isMatch {
				tui.Printf("Possible match: '%s' ⟶ '%s - %s'", filepath.Base(filePath), track.Artists[0], track.Title)

				// Ask user for confirmation
				tui.Printf("Would you like to update this file with Spotify metadata? (y/n)")
				confirmation := tui.Reads("Confirm (y/n):")

				if strings.ToLower(confirmation) == "y" {
					// Update the ID3 tags
					tag, err := id3.Open(filePath, id3v2.Options{Parse: true})
					if err != nil {
						tui.Printf("Failed to open file for tagging: %s", err)
						continue
					}

					tag.SetSpotifyID(track.ID)
					tag.SetTitle(track.Title)
					tag.SetArtist(track.Artists[0])
					tag.SetAlbum(track.Album)
					tag.SetArtworkURL(track.Artwork.URL)
					tag.SetDuration(strconv.Itoa(track.Duration))
					tag.SetTrackNumber(strconv.Itoa(track.Number))
					tag.SetYear(strconv.Itoa(track.Year))

					if err := tag.Save(); err != nil {
						tui.Printf("Failed to save tags: %s", err)
						tag.Close()
						continue
					}

					tag.Close()

					// Update the index
					indexData.SetPath(filePath, index.Installed)

					// Remove from our map so we don't try to match it again
					delete(filesWithoutIDs, filePath)

					tui.Printf("Successfully updated tags for: %s", filepath.Base(filePath))
				}
			}
		}
	}

	return nil
}

// indexer scans a possible local music library
// to be considered as already synchronized
func routineIndex(path string) func(context.Context, chan error) {
	return func(_ context.Context, ch chan error) {
		// remember to signal fetcher
		defer close(routineSemaphores[routineTypeIndex])

		tui.Lot("index").Printf("scanning")
		if err := indexData.Build(path); err != nil {
			tui.Printf("indexing failed: %s", err)
			routineSemaphores[routineTypeIndex] <- false
			ch <- err
			return
		}
		tui.Lot("index").Printf("%d tracks indexed", indexData.Size())

		// Before we signal that indexing is complete, check if we should
		// try to match local files without Spotify IDs to Spotify tracks
		spotifyClient, err := spotify.Authenticate(spotify.BrowserProcessor)
		if err != nil {
			tui.Printf("authentication for fuzzy matching failed: %s", err)
			tui.Lot("index").Close(strconv.Itoa(indexData.Size()) + " tracks")
			routineSemaphores[routineTypeIndex] <- true
			return
		}

		// Get some tracks from the user's library to try to match against
		tracksChan := make(chan interface{}, 1000)
		trackList := []*entity.Track{}

		// Fetch tracks from the user's Spotify library
		go func() {
			if err := spotifyClient.Library(100, tracksChan); err != nil {
				tui.Printf("Error fetching library: %s", err)
				close(tracksChan)
				return
			}
			close(tracksChan)
		}()

		// Collect tracks
		for track := range tracksChan {
			trackList = append(trackList, track.(*entity.Track))
		}

		// If we got tracks, try to match them against local files without Spotify IDs
		if len(trackList) > 0 {
			tui.Lot("index").Printf("attempting to match files with Spotify tracks")
			if err := fuzzyFindTracksForSpotify(path, trackList); err != nil {
				tui.Printf("Error during fuzzy matching: %s", err)
			}
		}

		tui.Lot("index").Close(strconv.Itoa(indexData.Size()) + " tracks")

		// once indexed, signal fetcher
		routineSemaphores[routineTypeIndex] <- true
	}
}

func routineAuth(_ context.Context, ch chan error) {
	// remember to close auth semaphore
	defer close(routineSemaphores[routineTypeAuth])

	tui.Lot("auth").Printf("authenticating")
	var err error
	spotifyClient, err = spotify.Authenticate(spotify.BrowserProcessor)
	if err != nil {
		tui.Printf("authentication failed: %s", err)
		routineSemaphores[routineTypeAuth] <- false
		ch <- err
		return
	}
	tui.Lot("auth").Close()

	// once authenticated, signal fetcher
	routineSemaphores[routineTypeAuth] <- true
}

// fetcher pulls data from the upstream
// provider, i.e. Spotify
func routineFetch(library bool, playlists, playlistsTracks, albums, tracks, fixes []string, libraryLimit int) func(ctx context.Context, ch chan error) {
	return func(_ context.Context, ch chan error) {
		// remember to stop passing data to decider and mixer
		defer close(routineQueues[routineTypeDecide])
		defer close(routineQueues[routineTypeMix])
		// block until indexing and authentication is done
		if !<-routineSemaphores[routineTypeIndex] {
			return
		}
		if !<-routineSemaphores[routineTypeAuth] {
			return
		}

		fetched := make(chan interface{}, 10000)
		defer close(fetched)
		go func() {
			counter := 0
			for event := range fetched {
				counter++
				track := event.(*entity.Track)
				tui.Lot("fetch").Printf("%s by %s", track.Title, track.Artists[0])
			}
			tui.Lot("fetch").Close(fmt.Sprintf("%d tracks", counter))
		}()

		fixesTracks, fixesErr := routineFetchFixesIDs(fixes)
		if fixesErr != nil {
			ch <- fixesErr
			return
		}
		tracks = append(tracks, fixesTracks...)

		if err := routineFetchLibrary(library, libraryLimit, fetched); err != nil {
			ch <- err
			return
		}
		if err := routineFetchAlbums(albums, fetched); err != nil {
			ch <- err
			return
		}
		if err := routineFetchTracks(tracks, fetched); err != nil {
			ch <- err
			return
		}
		if err := routineFetchPlaylists(append(playlists, playlistsTracks...), fetched); err != nil {
			ch <- err
			return
		}
	}
}

func routineFetchFixesIDs(fixes []string) ([]string, error) {
	var localTracks []string
	for _, path := range fixes {
		tui.Lot("fetch").Printf("track %s", path)
		tag, err := id3.Open(path, id3v2.Options{Parse: true})
		if err != nil {
			return nil, err
		}

		id := tag.SpotifyID()
		if len(id) == 0 {
			return nil, errors.New("track " + path + " does not have spotify ID metadata set")
		}

		localTracks = append(localTracks, id)
		indexData.SetPath(path, index.Flush)
		if err := tag.Close(); err != nil {
			return nil, err
		}
	}
	return localTracks, nil
}

func routineFetchLibrary(library bool, libraryLimit int, fetched chan interface{}) error {
	if !library {
		return nil
	}

	tui.Lot("fetch").Printf("library")
	return spotifyClient.Library(libraryLimit, routineQueues[routineTypeDecide], fetched)
}

func routineFetchAlbums(albums []string, fetched chan interface{}) error {
	for _, id := range albums {
		tui.Lot("fetch").Printf("album %s", id)
		if _, err := spotifyClient.Album(id, routineQueues[routineTypeDecide], fetched); err != nil {
			return err
		}
	}
	return nil
}

func routineFetchTracks(tracks []string, fetched chan interface{}) error {
	for _, id := range tracks {
		tui.Lot("fetch").Printf("track %s", id)
		if _, err := spotifyClient.Track(id, routineQueues[routineTypeDecide], fetched); err != nil {
			return err
		}
	}
	return nil
}

func routineFetchPlaylists(playlists []string, fetched chan interface{}) error {
	for index, id := range playlists {
		tui.Lot("fetch").Printf("playlist %s", id)
		playlist, err := spotifyClient.Playlist(id, routineQueues[routineTypeDecide], fetched)
		if err != nil {
			return err
		}
		if index < len(playlists) {
			routineQueues[routineTypeMix] <- playlist
		}
	}
	return nil
}

// decider finds the right asset to retrieve
// for a given track
func routineDecide(manualMode bool, outputDir string) func(context.Context, chan error) {
	return func(_ context.Context, ch chan error) {
		// remember to stop passing data to the collector
		// the retriever, the composer and the painter
		defer close(routineQueues[routineTypeCollect])

		for event := range routineQueues[routineTypeDecide] {
			track := event.(*entity.Track)

			// First check if we already have this track by Spotify ID
			if _, err := os.Stat(track.Path().Final()); err == nil {
				tag, err := id3.Open(track.Path().Final(), id3v2.Options{Parse: true})
				if err == nil {
					defer tag.Close()
					if existingID := tag.SpotifyID(); existingID == track.ID {
						tui.Printf("track %s already exists with matching ID: %s", track.Path().Final(), existingID)
						indexData.Set(track, index.Installed)
						continue
					} else if existingID != track.ID {
						tui.Printf("track %s has a different ID (%s) that the one in the playlist (%s). Updating ID...", track.Path().Final(), existingID, track.ID)
						tag.SetSpotifyID(track.ID)
						if err := tag.Save(); err != nil {
							tui.AnchorPrintf("failed to update tags: %s", err)
							tag.Close()
							continue
						}
						continue
					}
				}
			} else {
				tui.Printf("couldn't find track: %s", track.Path().Final())
			}

			if status, ok := indexData.Get(track); !ok {
				tui.Printf("sync %s by %s", track.Title, track.Artists[0])
				indexData.Set(track, index.Online)
			} else if status == index.Online {
				tui.Printf("skip %s by %s", track.Title, track.Artists[0])
				continue
			} else if status == index.Offline {
				tui.Printf("missing %s by %s", track.Title, track.Artists[0])
				continue
			}

			if manualMode {
				tui.Lot("decide").Printf("waiting on user input")
				tui.Printf("For track: %s by %s", track.Title, track.Artists[0])
				tui.Printf("0. Skip this track")
				tui.Printf("1. Download with URL")
				tui.Printf("2. Fuzzy search for local file (default)")
				choice := tui.Reads("Choose an option [2]:")

				// Default to option 2 if user just presses Enter
				if choice == "" {
					choice = "2"
					tui.Printf("Using default option: 2")
				}

				switch choice {
				case "0":
					// Skip this track
					tui.Printf("Skipping track: %s by %s", track.Title, track.Artists[0])
					tui.Lot("decide").Wipe()
					continue

				case "1":
					// Option 1: Download with URL
					url := tui.Reads("Enter URL:")
					tui.Lot("decide").Wipe()
					if len(url) == 0 {
						continue
					}
					track.UpstreamURL = url

				case "2":
					// Option 2: Fuzzy search for local file
					// Search the output directory for matching files
					tui.Printf("Searching for files matching: %s by %s", track.Title, track.Artists[0])

					// Get all files that might match
					matches, err := fuzzySearchLocalFiles(outputDir, track)
					if err != nil {
						tui.AnchorPrintf("search failed: %s", err)
						continue
					}

					if len(matches) == 0 {
						tui.Printf("No matching files found")
						continue
					}

					// Display numbered list of options
					tui.Printf("Found %d potential matches:", len(matches))
					for i, match := range matches {
						tui.Printf("%d. %s", i+1, filepath.Base(match))
					}

					// Let user select a file (with 1 as default)
					tui.Printf("Press Enter for #1 or select a file (1-%d) or 0 to cancel:", len(matches))
					selection := tui.Reads("Select [1]:")
					tui.Lot("decide").Wipe()

					// Parse selection, default to 1 if empty
					var selectionNum int
					if selection == "" {
						selectionNum = 1
						tui.Printf("Using default selection: 1")
					} else {
						var parseErr error
						selectionNum, parseErr = strconv.Atoi(selection)
						if parseErr != nil || selectionNum < 0 || selectionNum > len(matches) {
							tui.AnchorPrintf("invalid selection")
							continue
						}
					}

					// If user cancels
					if selectionNum == 0 {
						tui.Printf("Selection cancelled")
						continue
					}

					selectedFile := matches[selectionNum-1]

					// Update the file: rename it to match the expected format
					expectedPath := filepath.Join(filepath.Dir(selectedFile), track.Path().Final())

					tui.Printf("Renaming file from: %s to: %s", filepath.Base(selectedFile), filepath.Base(expectedPath))

					// Check and update tags first
					tag, err := id3.Open(selectedFile, id3v2.Options{Parse: true})
					if err != nil {
						tui.AnchorPrintf("failed to open selected file: %s", err)
						continue
					}

					// Update tags
					tag.SetSpotifyID(track.ID)
					tag.SetTitle(track.Title)
					tag.SetArtist(track.Artists[0])
					tag.SetAlbum(track.Album)
					tag.SetArtworkURL(track.Artwork.URL)
					tag.SetDuration(strconv.Itoa(track.Duration))
					tag.SetTrackNumber(strconv.Itoa(track.Number))
					tag.SetYear(strconv.Itoa(track.Year))

					if err := tag.Save(); err != nil {
						tui.AnchorPrintf("failed to update tags: %s", err)
						tag.Close()
						continue
					}

					tag.Close()

					// Rename the file
					if err := util.FileMoveOrCopy(selectedFile, expectedPath, true); err != nil {
						tui.AnchorPrintf("failed to rename file: %s", err)
						continue
					}

					// Update index
					indexData.Set(track, index.Installed)
					tui.Printf("File successfully renamed and tagged")
					continue

				default:
					tui.AnchorPrintf("invalid option, skipping track")
					continue
				}

			} else {
				tui.Lot("decide").Printf("%s by %s", track.Title, track.Artists[0])
				matches, err := provider.Search(track)
				tui.Lot("decide").Wipe()
				if err != nil {
					ch <- err
					return
				}

				if len(matches) == 0 {
					tui.AnchorPrintf("%s by %s (id: %s) not found", track.Title, track.Artists[0], track.ID)
					continue
				}
				track.UpstreamURL = matches[0].URL
			}
			routineQueues[routineTypeCollect] <- track
		}
		tui.Lot("decide").Close()
	}
}

// fuzzySearchLocalFiles searches for files in the given directory that might match the track
func fuzzySearchLocalFiles(dir string, track *entity.Track) ([]string, error) {
	var results []string

	// Clean and prepare search terms for better matching
	cleanTerm := func(input string) string {
		// Remove common separators and words that might be formatted differently
		result := strings.ToLower(input)
		result = strings.ReplaceAll(result, "&", "and")
		result = strings.ReplaceAll(result, " - ", " ")
		result = strings.ReplaceAll(result, " - ", " ")
		result = strings.ReplaceAll(result, "-", "")
		result = strings.ReplaceAll(result, "_", " ")
		result = strings.ReplaceAll(result, "(", " ")
		result = strings.ReplaceAll(result, ")", " ")
		result = strings.ReplaceAll(result, "[", " ")
		result = strings.ReplaceAll(result, "]", " ")
		result = strings.ReplaceAll(result, "feat.", " ")
		result = strings.ReplaceAll(result, "feat", " ")
		result = strings.ReplaceAll(result, "ft.", " ")
		result = strings.ReplaceAll(result, "ft", " ")
		// Remove common genre/remix indicators that might be formatted differently
		result = strings.ReplaceAll(result, " mix", " ")
		result = strings.ReplaceAll(result, " remix", " ")
		result = strings.ReplaceAll(result, " edit", " ")
		result = strings.ReplaceAll(result, " version", " ")
		result = strings.ReplaceAll(result, " original", " ")
		// Clean up extra spaces
		result = strings.TrimSpace(result)
		// Replace multiple spaces with a single space
		space := regexp.MustCompile(`\s+`)
		result = space.ReplaceAllString(result, " ")
		return result
	}

	// Get base title without any remix information
	baseTitle := track.Song()

	// Create a special filename pattern that matches artist-title pattern
	expectedFilenamePattern := fmt.Sprintf("%s - %s",
		regexp.QuoteMeta(strings.ReplaceAll(track.Artists[0], ".", "")),
		regexp.QuoteMeta(baseTitle))

	// Clean the terms for matching
	cleanedTitle := cleanTerm(track.Title)
	cleanedBaseTitle := cleanTerm(baseTitle)
	cleanedArtist := cleanTerm(track.Artists[0])

	// Words to extract from title and artist for individual word matching
	titleWords := strings.Fields(cleanedTitle)
	artistWords := strings.Fields(cleanedArtist)

	// Debug info
	// tui.Printf("Searching for '%s - %s' (base title: '%s')", track.Artists[0], track.Title, baseTitle)

	type ScoredMatch struct {
		Path  string
		Score int
	}
	var scoredMatches []ScoredMatch

	// Walk through the directory
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		// Skip directories and non-music files
		if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(path), entity.TrackFormat) {
			return nil
		}

		// Get filename without extension
		fileName := filepath.Base(path)
		fileNameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		cleanedFileName := cleanTerm(fileName)

		// Calculate match score
		score := 0

		// Check for exact artist-title pattern (highest priority - 100 points)
		re := regexp.MustCompile("(?i)" + expectedFilenamePattern)
		if re.MatchString(fileName) {
			score += 100
			// tui.Printf("Exact pattern match: %s", fileName)
		}

		// Check for artist appearing in filename (high priority - 50 points)
		artistRe := regexp.MustCompile("(?i)" + regexp.QuoteMeta(track.Artists[0]))
		if artistRe.MatchString(fileName) {
			score += 50
		}

		// Check for base title appearing in filename (high priority - 40 points)
		baseTitleRe := regexp.MustCompile("(?i)" + regexp.QuoteMeta(baseTitle))
		if baseTitleRe.MatchString(fileName) {
			score += 40
		}

		// Check for artist-title format (Artist - Title) - 30 points
		if strings.Contains(fileNameWithoutExt, " - ") {
			parts := strings.SplitN(fileNameWithoutExt, " - ", 2)
			artistPart := cleanTerm(parts[0])
			titlePart := cleanTerm(parts[1])

			// If parts match artist and base title, give high score
			if strings.Contains(artistPart, cleanedArtist) && strings.Contains(titlePart, cleanedBaseTitle) {
				score += 30
			}
		}

		// Award points for matching individual significant words from title and artist
		for _, word := range titleWords {
			if len(word) > 3 && strings.Contains(cleanedFileName, word) {
				score += 5
			}
		}

		for _, word := range artistWords {
			if len(word) > 3 && strings.Contains(cleanedFileName, word) {
				score += 5
			}
		}

		// If we have any score, add it to our results
		if score > 0 {
			scoredMatches = append(scoredMatches, ScoredMatch{Path: path, Score: score})
		}

		return nil
	})

	// Sort results by score (highest first)
	sort.Slice(scoredMatches, func(i, j int) bool {
		return scoredMatches[i].Score > scoredMatches[j].Score
	})

	// Take top 10 results
	for i, match := range scoredMatches {
		if i >= 10 {
			break
		}
		results = append(results, match.Path)
		// tui.Printf("Match: %s (score: %d)", filepath.Base(match.Path), match.Score)
	}

	return results, err
}

// collector fetches all the needed assets
// for a blob to be processed (basically
// a wrapper around: retriever, composer and painter)
func routineCollect(lyrics bool) func(context.Context, chan error) {
	return func(_ context.Context, ch chan error) {
		// remember to stop passing data to installer
		defer close(routineQueues[routineTypeProcess])

		for event := range routineQueues[routineTypeCollect] {
			track := event.(*entity.Track)
			if lyrics {
				if err := nursery.RunConcurrently(
					routineCollectAsset(track),
					routineCollectLyrics(track),
					routineCollectArtwork(track),
				); err != nil {
					tui.Printf("failure in routineCollect")
					ch <- err
					return
				}
			} else {
				if err := nursery.RunConcurrently(
					routineCollectAsset(track),
					routineCollectArtwork(track),
				); err != nil {
					ch <- err
					return
				}
			}
			routineQueues[routineTypeProcess] <- track
		}
		tui.Lot("download").Close()
		tui.Lot("compose").Close()
		tui.Lot("paint").Close()
	}
}

// retriever pulls a track blob corresponding
// to the (meta)data fetched from upstream
func routineCollectAsset(track *entity.Track) func(context.Context, chan error) {
	return func(_ context.Context, ch chan error) {
		tui.Lot("download").Print(track.UpstreamURL)
		if err := downloader.Download(track.UpstreamURL, track.Path().Download(), nil); err != nil {
			tui.AnchorPrintf("download failure: %s", err)
			ch <- err
			return
		}
		tui.Printf("asset for %s by %s: %s", track.Title, track.Artists[0], track.UpstreamURL)
		tui.Lot("download").Wipe()
	}
}

// composer pulls lyrics to be inserted
// in the fetched blob
func routineCollectLyrics(track *entity.Track) func(context.Context, chan error) {
	return func(_ context.Context, ch chan error) {
		tui.Lot("compose").Printf("%s by %s", track.Title, track.Artists[0])
		lyrics, err := lyrics.Search(track)
		if err != nil {
			tui.AnchorPrintf("compose failure: %s", err)
			ch <- err
			return
		}
		tui.Lot("compose").Wipe()
		track.Lyrics = lyrics
		tui.Printf("lyrics for %s by %s: %s", track.Title, track.Artists[0], util.Excerpt(lyrics))
	}
}

// painter pulls image blobs to be inserted
// as artworks in the fetched blob
func routineCollectArtwork(track *entity.Track) func(context.Context, chan error) {
	return func(_ context.Context, ch chan error) {
		artwork := make(chan []byte, 1)
		defer close(artwork)

		tui.Lot("paint").Printf("%s by %s", track.Title, track.Artists[0])
		if err := downloader.Download(track.Artwork.URL, track.Path().Artwork(), processor.Artwork{}, artwork); err != nil {
			tui.AnchorPrintf("compose failure: %s", err)
			ch <- err
			return
		}

		tui.Lot("paint").Wipe()
		track.Artwork.Data = <-artwork
		tui.Printf("artwork for %s by %s: %s", track.Title, track.Artists[0], util.HumanizeBytes(len(track.Artwork.Data)))
	}
}

// postprocessor applies some further enhancements
// e.g. combining the downloaded artwork/lyrics
// into the blob
func routineProcess(_ context.Context, ch chan error) {
	// remember to stop passing data to installer
	defer close(routineQueues[routineTypeInstall])

	for event := range routineQueues[routineTypeProcess] {
		track := event.(*entity.Track)
		tui.Lot("process").Printf("%s by %s", track.Title, track.Artists[0])
		if err := processor.Do(track); err != nil {
			tui.AnchorPrintf("processing failed for %s by %s: %s", track.Title, track.Artists[0], err)
			ch <- err
			return
		}
		tui.Lot("process").Wipe()
		routineQueues[routineTypeInstall] <- track
	}
	tui.Lot("process").Close()
}

// installer move the blob to its final destination
func routineInstall(_ context.Context, ch chan error) {
	// remember to signal mixer
	defer close(routineSemaphores[routineTypeInstall])

	for event := range routineQueues[routineTypeInstall] {
		var (
			track     = event.(*entity.Track)
			status, _ = indexData.Get(track)
		)
		tui.Lot("install").Printf("%s by %s ", track.Title, track.Artists[0])
		if err := util.FileMoveOrCopy(track.Path().Download(), track.Path().Final(), status == index.Flush); err != nil {
			tui.AnchorPrintf("installation failed for %s by %s: %s", track.Title, track.Artists[0], err)
			ch <- err
			return
		}
		tui.Lot("install").Wipe()
		indexData.Set(track, index.Installed)
	}
	tui.Lot("install").Close(strconv.Itoa(indexData.Size(index.Installed)) + " tracks")
}

// mixer wraps playlists to their final destination
func routineMix(encoding string) func(context.Context, chan error) {
	return func(_ context.Context, ch chan error) {
		// block until installation is done
		<-routineSemaphores[routineTypeInstall]

		counter := 0
		for event := range routineQueues[routineTypeMix] {
			counter++
			playlist := event.(*playlist.Playlist)
			tui.Lot("mix").Printf("%s", playlist.Name)
			encoder, err := playlist.Encoder(encoding)
			if err != nil {
				tui.AnchorPrintf("mixing failed for %s: %s", playlist.Name, err)
				ch <- err
				return
			}

			for _, track := range playlist.Tracks {
				if trackStatus, ok := indexData.Get(track); !ok || (trackStatus != index.Installed && trackStatus != index.Offline) {
					continue
				}

				if err := encoder.Add(track); err != nil {
					tui.AnchorPrintf("adding track to %s failed: %s", playlist.Name, err)
					ch <- err
					return

				}
			}

			if err := encoder.Close(); err != nil {
				tui.AnchorPrintf("closing playlist %s failed: %s", playlist.Name, err)
				ch <- err
				return
			}
		}
		tui.Lot("mix").Close(fmt.Sprintf("%d playlists", counter))
	}
}
