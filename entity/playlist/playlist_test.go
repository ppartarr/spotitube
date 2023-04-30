package playlist

import (
	"errors"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/streambinder/spotitube/entity"
	"github.com/streambinder/spotitube/util"
	"github.com/stretchr/testify/assert"
)

var (
	testTrack = &entity.Track{
		Title:   "Title",
		Artists: []string{"Artist"},
	}
	testPlaylist = &Playlist{
		Name:   "Playlist",
		Tracks: []*entity.Track{testTrack},
	}
)

func TestEncoderM3U(t *testing.T) {
	assert.Nil(t, util.ErrOnly(testPlaylist.Encoder("m3u")))
}

func TestEncoderInitFailure(t *testing.T) {
	// monkey patching
	defer gomonkey.ApplyPrivateMethod(&M3UEncoder{}, "init", func() error {
		return errors.New("ko")
	}).Reset()

	// testing
	assert.Error(t, util.ErrOnly(testPlaylist.Encoder("m3u")), "ko")
}

func TestEncoderUnknown(t *testing.T) {
	assert.Error(t, util.ErrOnly(testPlaylist.Encoder("wut")), "unsupported encoding")
}