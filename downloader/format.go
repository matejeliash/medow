package downloader

import "fmt"

// format eta from seconds to HH:MM:SS
func FormatEta(secs int64) string {
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func FormatSpeed(bps float64) string {
	if bps > 1_000_000 {
		return fmt.Sprintf("%.2f MB/s", bps/1_000_000)

	} else if bps > 1_000 {
		return fmt.Sprintf("%.2f KB/s", bps/1_000)

	} else {
		return fmt.Sprintf("%.2f B/s", bps)

	}

}

func PrintFormattedInfo(current, totalSize int64, bps float64, eta int64) {

	percent := float64(current) / float64(totalSize) * 100

	fmt.Printf("\rProgress: %.2f%% %d/%d MB  DS: %s ETA: %s",
		percent,
		current/1_000_000,
		totalSize/1_000_000,
		FormatSpeed(bps),
		FormatEta(eta),
	)

}
