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

	PassedMilliSc int64

	OutputFile   *os.File
	ProgressFile *os.File

	BufferSize int64 // size of buffer for chunks received from server
}

//const bufferSize = 8192

// create new Downloader object
func NewDownloader(url string, filepath string, useProgressFile bool) *Downloader {
	d := &Downloader{}
	d.FilePath = filepath
	d.Url = url
	d.ProgressPath = filepath + ".progress"
	d.UseProgressFile = useProgressFile
	d.BufferSize = 32768
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
	buf := make([]byte, d.BufferSize)

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

func (d *Downloader) DownloadChunksWithLimit(body io.Reader, maxSpeedBytes int64) error {
	buf := make([]byte, d.BufferSize)

	startTime := time.Now()
	var totalBytes int64 = 0

	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			_, writeErr := d.OutputFile.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			atomic.AddInt64(&d.Downloaded, int64(n))
			totalBytes += int64(n)

			// Calculate expected duration for totalBytes
			expectedDuration := time.Duration(float64(totalBytes)/float64(maxSpeedBytes)) * time.Second
			elapsed := time.Since(startTime)

			if sleepDuration := expectedDuration - elapsed; sleepDuration > 0 {
				time.Sleep(sleepDuration)
			}
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
		fmt.Println("Server doesn't support partial downloads, please remove file: ", d.ProgressPath)

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
		fmt.Println("Download completed.")
	}

	return err

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
				// load downloaded byte count
				current := atomic.LoadInt64(&d.Downloaded)
				// load passed milliseconds
				atomic.AddInt64(&d.PassedMilliSc, 1000)
				passedSecs := float64(atomic.LoadInt64(&d.PassedMilliSc)) / 1000.0

				if d.TotalSize > 0 {
					var bps float64 = 0
					// also remove d.ResumedAt which is loaded when downloading is resumed
					if passedSecs > 0 {
						bps = float64(current-d.ResumedAt) / passedSecs // speed is byte/s
					}

					var eta int64 = 0
					if bps > 0 {
						eta = int64(float64(d.TotalSize-current) / bps)

					}

					PrintFormattedInfo(current, d.TotalSize, bps, eta)

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
