package utils

import (
	"os"

	"github.com/saintfish/chardet"
)

func getCharDet(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	firstBytes := make([]byte, 512)

	n, err := f.Read(firstBytes)
	if err != nil {
		return "", err
	}

	det := chardet.NewTextDetector()
	charGuess, err := det.DetectBest(firstBytes[:n])

	return charGuess.Charset, err
}
