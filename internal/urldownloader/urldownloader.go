package urldownloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/alexballas/go2tv/internal/utils"
)

type TFile struct {
	F *os.File
}

// NewDownloadURL - Start the URL media downloading and
// return an *os.File entity
func NewDownloadURL(ctx context.Context, s string) (*TFile, error) {
	// We don't want to close the file here as we'll keep
	// sending data to the file while we read it. In order to
	// properly close the file handler, we need to call
	// the *TFile Close() method.
	f, err := os.CreateTemp("", "media*.dat")
	if err != nil {
		return nil, err
	}

	go func() {
		client := http.Client{}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s, nil)
		if err != nil {
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		io.Copy(f, resp.Body)
	}()

	out := TFile{F: f}

	return &out, nil
}

func (f *TFile) Close() {
	fmt.Println("Deleting:", f.F.Name())
	// Close the underlying filehandler
	f.F.Close()
	os.Remove(f.F.Name())
}

func (f *TFile) WaitForValidMedia() error {
	ticker := time.NewTicker(200 * time.Millisecond)

	for {
		select {
		case <-ticker.C:
			out, err := utils.GetMimeDetailsFromFile(f.F.Name())
			if err != nil {
				return fmt.Errorf("failed to get valid media mime data: %w", err)
			}

			if out != "/" {
				return nil
			}

		case <-time.After(10 * time.Second):
			return errors.New("failed to wait for valid media for the URL")
		}
	}
}
