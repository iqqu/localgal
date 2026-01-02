//go:build placeholders

package main

import (
	_ "embed"
	"golocalgal/server"
	"log"
)

//go:embed embed/template_videos.tar.gz
var videosTarGz []byte

func init() {
	err := server.LoadPlaceholderVideos(videosTarGz)
	if err != nil {
		log.Fatal(err)
	}
}
