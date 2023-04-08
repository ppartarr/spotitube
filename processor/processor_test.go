package processor

import (
	"errors"
	"reflect"
	"testing"

	"bou.ke/monkey"
	"github.com/streambinder/spotitube/entity"
	"github.com/stretchr/testify/assert"
)

var track = &entity.Track{
	ID:         "123",
	Title:      "Title",
	Artists:    []string{"Artist"},
	Album:      "Album",
	ArtworkURL: "http://ima.ge",
	Duration:   180,
	Number:     1,
	Year:       "1970",
}

func TestProcessorDo(t *testing.T) {
	// monkey patching
	monkey.PatchInstanceMethod(reflect.TypeOf(normalizer{}), "Do",
		func(normalizer, *entity.Track) error {
			return nil
		})
	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(normalizer{}), "Do")
	monkey.PatchInstanceMethod(reflect.TypeOf(encoder{}), "Do",
		func(encoder, *entity.Track) error {
			return nil
		})
	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(encoder{}), "Do")

	// testing
	assert.Nil(t, Do(track))
}

func TestProcessorDoFailure(t *testing.T) {
	// monkey patching
	monkey.PatchInstanceMethod(reflect.TypeOf(normalizer{}), "Do",
		func(normalizer, *entity.Track) error {
			return nil
		})
	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(normalizer{}), "Do")
	monkey.PatchInstanceMethod(reflect.TypeOf(encoder{}), "Do",
		func(encoder, *entity.Track) error {
			return errors.New("failure")
		})
	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(encoder{}), "Do")

	// testing
	assert.Error(t, Do(track), "failure")
}
