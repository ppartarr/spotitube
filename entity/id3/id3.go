package id3

import (
	"strings"

	"github.com/bogem/id3v2/v2"
)

const (
	frameAttachedPicture      = "Attached picture"
	frameTrackNumber          = "Track number/Position in set"
	frameUnsynchronizedLyrics = "Unsynchronised lyrics/text transcription"
	frameSpotifyID            = "Spotify ID"
	frameArtworkURL           = "Artwork URL"
	frameDuration             = "Duration"
	frameUpstreamURL          = "Upstream URL"
)

type Tag struct {
	id3v2.Tag
	Cache map[string]string
}

func Open(path string, options id3v2.Options) (*Tag, error) {
	tag, err := id3v2.Open(path, options)
	if err != nil {
		return nil, err
	}
	return &Tag{*tag, make(map[string]string)}, err
}

func (tag *Tag) SetTrackNumber(number string) {
	tag.AddFrame(
		tag.CommonID(frameTrackNumber),
		id3v2.TextFrame{
			Encoding: tag.DefaultEncoding(),
			Text:     number,
		},
	)
}

func (tag *Tag) TrackNumber() string {
	return tag.GetTextFrame(tag.CommonID(frameTrackNumber)).Text
}

func (tag *Tag) setUserDefinedText(key, value string) {
	tag.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{
		Encoding:    tag.DefaultEncoding(),
		Description: key,
		Value:       value,
	})
}

func (tag *Tag) userDefinedText(key string) string {
	if value, ok := tag.Cache[key]; ok {
		return value
	}

	for _, frame := range tag.GetFrames(tag.CommonID("User defined text information frame")) {
		frame, ok := frame.(id3v2.UserDefinedTextFrame)
		if ok {
			tag.Cache[frame.UniqueIdentifier()] = frame.Value
		}

		if strings.EqualFold(frame.UniqueIdentifier(), key) {
			return frame.Value
		}
	}

	return ""
}

func (tag *Tag) SetSpotifyID(id string) {
	tag.setUserDefinedText(frameSpotifyID, id)
}

func (tag *Tag) SpotifyID() string {
	return tag.userDefinedText(frameSpotifyID)
}

func (tag *Tag) SetArtworkURL(url string) {
	tag.setUserDefinedText(frameArtworkURL, url)
}

func (tag *Tag) ArtworkURL() string {
	return tag.userDefinedText(frameArtworkURL)
}

func (tag *Tag) SetDuration(duration string) {
	tag.setUserDefinedText(frameDuration, duration)
}

func (tag *Tag) Duration() string {
	return tag.userDefinedText(frameDuration)
}

func (tag *Tag) SetUpstreamURL(url string) {
	tag.setUserDefinedText(frameUpstreamURL, url)
}

func (tag *Tag) UpstreamURL() string {
	return tag.userDefinedText(frameUpstreamURL)
}

func (tag *Tag) SetAttachedPicture(picture []byte) {
	tag.AddAttachedPicture(id3v2.PictureFrame{
		Encoding:    tag.DefaultEncoding(),
		MimeType:    "image/jpeg",
		PictureType: id3v2.PTFrontCover,
		Description: "Front cover",
		Picture:     picture,
	})
}

func (tag *Tag) AttachedPicture() (string, []byte) {
	frame, ok := tag.GetLastFrame(tag.CommonID(frameAttachedPicture)).(id3v2.PictureFrame)
	if ok {
		return frame.MimeType, frame.Picture
	}
	return "", []byte{}
}

func (tag *Tag) SetUnsynchronizedLyrics(title, lyrics string) {
	tag.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
		Encoding:          tag.DefaultEncoding(),
		Language:          "eng",
		ContentDescriptor: title,
		Lyrics:            lyrics,
	})
}

func (tag *Tag) UnsynchronizedLyrics() string {
	frame, ok := tag.GetLastFrame(tag.CommonID(frameUnsynchronizedLyrics)).(id3v2.UnsynchronisedLyricsFrame)
	if ok {
		return frame.Lyrics
	}
	return ""
}
