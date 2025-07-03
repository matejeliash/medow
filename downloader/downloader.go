package downloader

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

type Downloader struct {
	Url             string // url to download from
	FilePath        string // path of file where data are written to
	ProgressPath    string // path of file where number of already downloaded bytes is stored
	UseProgressFile bool   // true signals to use progress file

	Downloaded int64 // already downloaded bytes
	TotalSize  int64 // full byte size of file
	ResumedAt  int64

	PassedMilliSecs int64

	OutputFile   *os.File
	ProgressFile *os.File
}

const bufferSize = 8192

// create new Downloader object
func NewDownloader(url string, filepath string, useProgressFile bool) *Downloader {
	d := &Downloader{}
	d.FilePath = filepath
	d.Url = url
	d.ProgressPath = filepath + ".progress"
	d.UseProgressFile = useProgressFile
	return d

}

// reads progress from .progress file if progress is enabled
func (d *Downloader) ReadProgress() {
	data, err := os.ReadFile(d.ProgressPath)
	if err != nil {
		d.Downloaded = 0
		return
	}
	parsedNum, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil || parsedNum < 0 {
		d.Downloaded = 0
		return
	}
	d.Downloaded = parsedNum
	d.ResumedAt = d.Downloaded

}

// creation of GET request based on input url
func (d *Downloader) CreateRequest() (*http.Request, error) {

	req, err := http.NewRequest("GET", d.Url, nil)
	if err != nil {
		return nil, err
	}

	// read progress file if enabled and ask server to send chunks from selected position
	if d.UseProgressFile {
		d.ReadProgress()
		if d.Downloaded > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", d.Downloaded))
		}

	}

	return req, nil

}

// download chunk of file from server
func (d *Downloader) DownloadChunks(body io.Reader) error {
	buf := make([]byte, bufferSize)

	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			_, writeErr := d.OutputFile.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			atomic.AddInt64(&d.Downloaded, int64(n))
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}

}

// this function actually starts and manages downloading
func (d *Downloader) Download() error {

	// create HTTP client & request
	httpClient := &http.Client{}
	req, err := d.CreateRequest()
	if err != nil {
		return err
	}
	// perform HTTP request
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	// force quit when servers response in negative
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("bad HTTP status %s\n", resp.Status)
	}

	// force quit when server doesn't support partial downloads
	if d.Downloaded > 0 && resp.StatusCode != http.StatusPartialContent {
		fmt.Println("Server doesn't support partial downloads, pelase remove file: ", d.ProgressPath)

		return fmt.Errorf("Server does not support partial downloads, if you want to continue please remove file: %s\n", d.ProgressPath)

	}
	// get file size from http header that was send by server
	contentLenStr := resp.Header.Get("Content-Length")
	if contentLenStr != "" {
		contentLen, err := strconv.ParseInt(contentLenStr, 10, 64)
		if err != nil {
			return err
		}
		d.TotalSize = d.Downloaded + contentLen
	}

	// open output file for writing and also prepare closing
	d.OutputFile, err = os.OpenFile(d.FilePath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer d.OutputFile.Close()

	// seek to last downloaded byte in output file
	_, err = d.OutputFile.Seek(d.Downloaded, 0)
	if err != nil {
		return err
	}

	// open progress file for writing and also prepare closing
	d.ProgressFile, err = os.OpenFile(d.ProgressPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer d.ProgressFile.Close()

	stopChan := make(chan struct{}) // this channel signal end of downloading
	d.ManageProgressPrinter(stopChan)

	fmt.Printf("Downloading from: %s\n", d.Url)
	fmt.Printf("Downloading to: ./%s\n", d.FilePath)

	// download all file chunks
	err = d.DownloadChunks(resp.Body)

	if err == nil {
		os.Remove(d.ProgressPath)
		fmt.Println("Download copleted.")
	}

	return err

}

// format eta from seconds to HH:MM:SS
func FormatEta(secs int) string {
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// this function manages printing of downloading progress, it prints the progress
// every second and also update progress file every second
func (d *Downloader) ManageProgressPrinter(stopChan chan struct{}) {

	ticker := time.NewTicker(1000 * time.Millisecond)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				current := atomic.LoadInt64(&d.Downloaded)
				atomic.AddInt64(&d.PassedMilliSecs, 1000)
				passedSecs := float64(atomic.LoadInt64(&d.PassedMilliSecs)) / 1000.0

				if d.TotalSize > 0 {
					percent := float64(current) / float64(d.TotalSize) * 100
					speed := float64(current-d.ResumedAt) / passedSecs // MB/s
					eta := 0
					if speed > 0 {
						eta = int(float64(d.TotalSize-current) / speed)

					}

					fmt.Printf("\rProgress: %.2f%% %d/%d MB  DS: %.2f MB/s ETA: %s",
						percent,
						current/1_000_000,
						d.TotalSize/1_000_000,
						speed/1_000_000,
						FormatEta(eta),
					)

				}

				if d.ProgressFile != nil {
					d.ProgressFile.Seek(0, 0)
					d.ProgressFile.Truncate(0)
					d.ProgressFile.WriteString(fmt.Sprintf("%d", current))
					d.ProgressFile.Sync()

				}

			case <-stopChan:
				return
			}
		}

	}()
}
