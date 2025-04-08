package cmd

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/streambinder/spotitube/util"
)

func YouTubeDl(url, path string) error {
	var (
		output bytes.Buffer
		ext    = filepath.Ext(path)[1:]
		stem   = strings.TrimSuffix(util.FileBaseStem(path), "."+ext)
		cmd    = exec.Command("yt-dlp",
			"--format", "bestaudio",
			"--extract-audio",
			"--audio-format", ext,
			"--audio-quality", "0",
			"--output", stem+".%(ext)s",
			"--continue",
			"--no-overwrites",
			"--retry-sleep", "exp=1::2",
			"--sleep-interval", "5",
			url,
		)
	)
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return errors.New(output.String())
	}
	return nil
}
