package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/streambinder/spotitube/entity/index"
	"github.com/streambinder/spotitube/spotify"
)

var (
	spotifyClient *spotify.Client
	cmdRoot       = &cobra.Command{
		Use:   "spotitube",
		Short: "Synchronize Spotify collections downloading from external providers",
	}
	indexData = index.New()
)

func Execute() {
	if err := cmdRoot.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
