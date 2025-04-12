package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/adrg/xdg"
	"github.com/bogem/id3v2/v2"
	"github.com/spf13/cobra"
	"github.com/streambinder/spotitube/downloader"
	"github.com/streambinder/spotitube/entity/id3"
	"github.com/streambinder/spotitube/entity/index"
	"github.com/streambinder/spotitube/processor"
	spotitubify "github.com/streambinder/spotitube/spotify"
	"github.com/streambinder/spotitube/util"
	"github.com/zmb3/spotify/v2"
)

func init() {
	cmdRoot.AddCommand(cmdInit())
}

func cmdInit() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Init ID3v2 data for local library",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var dir = util.ErrWrap(xdg.UserDirs.Music)(cmd.Flags().GetString("library"))

			client, err := authenticateSpotify()

			if err != nil {
				log.Fatalf("Failed to authenticate with Spotify: %v", err)
			}

			err = processDirectory(dir, client)
			if err != nil {
				log.Fatalf("Error processing directory: %v", err)
			}

			return nil
		},
	}
	cmd.Flags().StringP("library", "l", xdg.UserDirs.Music, "Path to music library")
	return cmd
}

func authenticateSpotify() (*spotitubify.Client, error) {
	client, err := spotitubify.Authenticate(spotitubify.BrowserProcessor)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func processDirectory(dir string, client *spotitubify.Client) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp3") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		fmt.Printf("Processing file: %s\n", filePath)

		err := processFile(filePath, client)
		if err != nil {
			log.Printf("Error processing file %s: %v\n", filePath, err)
		}
	}
	return nil
}

func processFile(filePath string, client *spotitubify.Client) error {
	// Open the MP3 file to check for existing ID3v2 tags
	mp3File, err := id3.Open(filePath, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("failed to open mp3 file: %v", err)
	}
	defer mp3File.Close()

	if id := mp3File.SpotifyID(); len(id) > 0 {
		log.Printf("File already has a spotify id tags, skipping: %s\n", filePath)
		return nil
	}

	// Extract artist and title from the file name
	fileName := filepath.Base(filePath)
	parts := strings.SplitN(fileName, " - ", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid file name format: %s", fileName)
	}
	artist := strings.TrimSuffix(parts[0], ".mp3")
	title := strings.TrimSuffix(parts[1], ".mp3")

	// Remove any "(ft ...)" or similar patterns from the title
	title = removeFeaturingInfo(title)

	// Search Spotify for the track
	results, err := searchSpotify(client.Client, artist, title)
	if err != nil {
		return fmt.Errorf("error searching Spotify: %v", err)
	}

	var track *spotify.FullTrack
	if len(results) > 0 && results[0].Name == title {
		track = results[0]
	} else {
		// No exact match, prompt user
		track, err = promptForTrack(client.Client, artist, title, results)
		if err != nil {
			return fmt.Errorf("error during manual track selection: %v", err)
		}
		if track == nil {
			// User chose to skip
			log.Printf("Skipped file: %s\n", fileName)
			return nil
		}
	}

	// Update MP3 tags
	return updateMP3Tags(client, filePath, track)
}

func removeFeaturingInfo(title string) string {
	// Remove anything in parentheses from the title
	if idx := strings.Index(title, "("); idx != -1 {
		title = strings.TrimSpace(title[:idx])
	}
	return title
}

func searchSpotify(client *spotify.Client, artist, title string) ([]*spotify.FullTrack, error) {
	ctx := context.Background()
	query := fmt.Sprintf("artist:%s track:%s", artist, title)
	results, err := client.Search(ctx, query, spotify.SearchTypeTrack)
	if err != nil {
		return nil, err
	}

	tracks := []*spotify.FullTrack{}
	if results.Tracks != nil {
		for _, item := range results.Tracks.Tracks {
			tracks = append(tracks, &item)
		}
	}
	return tracks, nil
}

func promptForTrack(client *spotify.Client, artist, title string, results []*spotify.FullTrack) (*spotify.FullTrack, error) {
	fmt.Printf("No exact match found for '%s - %s'.\n", artist, title)
	if len(results) == 1 {
		return results[0], nil
	} else if len(results) > 1 {
		fmt.Println("Suggested tracks:")
		for i, track := range results {
			fmt.Printf("%d: %s - %s (Album: %s) [Spotify Link: https://open.spotify.com/track/%s]\n", i+1, track.Artists[0].Name, track.Name, track.Album.Name, track.ID)
		}
	}

	fmt.Println("Enter the number of the track to use, or leave empty to skip:")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}

	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(results) {
		return nil, errors.New("invalid choice")
	}

	return results[choice-1], nil
}

func updateMP3Tags(client *spotitubify.Client, filePath string, track *spotify.FullTrack) error {
	mp3File, err := id3.Open(filePath, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("failed to open mp3 file: %v", err)
	}
	defer mp3File.Close()

	spotifyTrack, err := client.Track(track.ID.String())
	if err != nil {
		return err
	}

	artwork := make(chan []byte, 1)
	defer close(artwork)
	if err := downloader.Download(
		spotifyTrack.Artwork.URL, spotifyTrack.Path().Artwork(),
		processor.Artwork{}, artwork); err != nil {
		return err
	}

	mp3File.SetSpotifyID(spotifyTrack.ID)
	mp3File.SetTitle(spotifyTrack.Title)
	mp3File.SetArtist(spotifyTrack.Artists[0])
	mp3File.SetAlbum(spotifyTrack.Album)
	mp3File.SetArtworkURL(spotifyTrack.Artwork.URL)
	mp3File.SetAttachedPicture(<-artwork)
	mp3File.SetDuration(strconv.Itoa(spotifyTrack.Duration))
	mp3File.SetTrackNumber(strconv.Itoa(spotifyTrack.Number))
	mp3File.SetYear(strconv.Itoa(spotifyTrack.Year))
	mp3File.SetUpstreamURL(spotifyTrack.UpstreamURL)

	if err := mp3File.Save(); err != nil {
		return fmt.Errorf("failed to save mp3 file: %v", err)
	}

	// Add the file to the index as Installed since we've set all the tags
	indexData.SetPath(filePath, index.Installed)

	fmt.Printf("Successfully updated tags for: %s\n", filePath)
	return nil
}
