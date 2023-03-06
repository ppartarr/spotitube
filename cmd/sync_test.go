package cmd

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"bou.ke/monkey"
	"github.com/streambinder/spotitube/entity"
	"github.com/streambinder/spotitube/spotify"
	"github.com/streambinder/spotitube/util"
	"github.com/streambinder/spotitube/util/cmd"
	"github.com/stretchr/testify/assert"
)

var (
	track = &entity.Track{
		ID:         "123",
		Title:      "Title",
		Artists:    []string{"Artist"},
		Album:      "Album",
		ArtworkURL: "http://ima.ge",
		Duration:   180,
		Genre:      "Genre",
		Number:     1,
		Year:       "1970",
	}
	playlist = &entity.Playlist{
		ID:     "123",
		Name:   "Playlist",
		Owner:  "Owner",
		Tracks: []*entity.Track{track},
	}
	album = &entity.Album{
		ID:      "123",
		Name:    "Album",
		Artists: []string{"Artist"},
		Tracks:  []*entity.Track{track},
	}
)

func init() {
	monkey.Patch(time.Sleep, func(time.Duration) {})
	monkey.Patch(cmd.Open, func(string, ...string) error { return nil })
	monkey.Patch(spotify.Authenticate, func(...string) (*spotify.Client, error) { return &spotify.Client{}, nil })
	monkey.PatchInstanceMethod(reflect.TypeOf(&spotify.Client{}), "Library",
		func(client *spotify.Client, channels ...chan interface{}) error {
			channels[0] <- track
			return nil
		})
	monkey.PatchInstanceMethod(reflect.TypeOf(&spotify.Client{}), "Playlist",
		func(client *spotify.Client, id string, channels ...chan interface{}) (*entity.Playlist, error) {
			channels[0] <- track
			return playlist, nil
		})
	monkey.PatchInstanceMethod(reflect.TypeOf(&spotify.Client{}), "Album",
		func(client *spotify.Client, id string, channels ...chan interface{}) (*entity.Album, error) {
			channels[0] <- track
			return album, nil
		})
	monkey.PatchInstanceMethod(reflect.TypeOf(&spotify.Client{}), "Track",
		func(client *spotify.Client, id string, channels ...chan interface{}) (*entity.Track, error) {
			channels[0] <- track
			return track, nil
		})
}

func TestCmdSync(t *testing.T) {
	assert.Nil(t, util.ErrOnly(testExecute("sync", "-l", "-p", "123", "-a", "123", "-t", "123")))
}

func TestCmdSyncLibraryAutoEnabled(t *testing.T) {
	err := util.ErrOnly(testExecute("sync"))
	assert.Nil(t, err)
	library, err := cmdSync.Flags().GetBool("library")
	assert.Nil(t, err)
	assert.True(t, library)
}

func TestCmdSyncAuthFailure(t *testing.T) {
	monkey.Patch(spotify.Authenticate, func(...string) (*spotify.Client, error) { return client, errors.New("failure") })
	assert.EqualError(t, util.ErrOnly(testExecute("sync")), "failure")
}

func TestCmdSyncLibraryFailure(t *testing.T) {
	monkey.PatchInstanceMethod(reflect.TypeOf(&spotify.Client{}), "Library",
		func(client *spotify.Client, channels ...chan interface{}) error {
			return errors.New("failure")
		})
	assert.EqualError(t, util.ErrOnly(testExecute("sync")), "failure")
}

func TestCmdSyncPlaylistFailure(t *testing.T) {
	monkey.PatchInstanceMethod(reflect.TypeOf(&spotify.Client{}), "Playlist",
		func(*spotify.Client, string, ...chan interface{}) (*entity.Playlist, error) {
			return nil, errors.New("failure")
		})
	assert.EqualError(t, util.ErrOnly(testExecute("sync", "-p", "123")), "failure")
}

func TestCmdSyncAlbumFailure(t *testing.T) {
	monkey.PatchInstanceMethod(reflect.TypeOf(&spotify.Client{}), "Album",
		func(*spotify.Client, string, ...chan interface{}) (*entity.Album, error) {
			return nil, errors.New("failure")
		})
	assert.EqualError(t, util.ErrOnly(testExecute("sync", "-a", "123")), "failure")
}

func TestCmdSyncTrackFailure(t *testing.T) {
	monkey.PatchInstanceMethod(reflect.TypeOf(&spotify.Client{}), "Track",
		func(*spotify.Client, string, ...chan interface{}) (*entity.Track, error) {
			return nil, errors.New("failure")
		})
	assert.EqualError(t, util.ErrOnly(testExecute("sync", "-t", "123")), "failure")
}
