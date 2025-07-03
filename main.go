package main

import (
	"os"

	"github.com/matejeliash/medow/downloader"
)

func main() {

	d := downloader.NewDownloader(os.Args[1], os.Args[2], true)
	d.Download()
}
