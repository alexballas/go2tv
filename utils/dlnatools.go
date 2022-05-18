package utils

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/h2non/filetype"
)

const (
	// dlnaOrgFlagSenderPaced = 1 << 31
	// dlnaOrgFlagTimeBasedSeek = 1 << 30
	// dlnaOrgFlagByteBasedSeek = 1 << 29
	// dlnaOrgFlagPlayContainer = 1 << 28
	// dlnaOrgFlagS0Increase = 1 << 27
	// dlnaOrgFlagSnIncrease = 1 << 26
	// dlnaOrgFlagRtspPause = 1 << 25
	dlnaOrgFlagStreamingTransferMode = 1 << 24
	// dlnaOrgFlagInteractiveTransfertMode = 1 << 23
	dlnaOrgFlagBackgroundTransfertMode = 1 << 22
	dlnaOrgFlagConnectionStall         = 1 << 21
	dlnaOrgFlagDlnaV15                 = 1 << 20
)

var (
	dlnaprofiles = map[string]string{
		"video/x-mkv":             "DLNA.ORG_PN=MATROSKA",
		"video/x-matroska":        "DLNA.ORG_PN=MATROSKA",
		"video/x-msvideo":         "DLNA.ORG_PN=AVI",
		"video/mpeg":              "DLNA.ORG_PN=MPEG1",
		"video/vnd.dlna.mpeg-tts": "DLNA.ORG_PN=MPEG1",
		"video/mp4":               "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/quicktime":         "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/x-m4v":             "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/3gpp":              "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/x-flv":             "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/x-ms-wmv":          "DLNA.ORG_PN=WMVHIGH_FULL",
		"audio/mpeg":              "DLNA.ORG_PN=MP3",
		"image/jpeg":              "JPEG_LRG",
		"image/png":               "PNG_LRG",
	}
)

func defaultStreamingFlags() string {
	return fmt.Sprintf("%.8x%.24x", dlnaOrgFlagStreamingTransferMode|
		dlnaOrgFlagBackgroundTransfertMode|
		dlnaOrgFlagConnectionStall|
		dlnaOrgFlagDlnaV15, 0)
}

// BuildContentFeatures builds the content features string
// for the "contentFeatures.dlna.org" header.
func BuildContentFeatures(mediaType string, seek string, transcode bool) (string, error) {
	var cf strings.Builder

	if mediaType != "" {
		dlnaProf, profExists := dlnaprofiles[mediaType]
		if profExists {
			cf.WriteString(dlnaProf + ";")
		}
	}

	// "00" neither time seek range nor range supported
	// "01" range supported
	// "10" time seek range supported
	// "11" both time seek range and range supported
	switch seek {
	case "00":
		cf.WriteString("DLNA.ORG_OP=00;")
	case "01":
		cf.WriteString("DLNA.ORG_OP=01;")
	case "10":
		cf.WriteString("DLNA.ORG_OP=10;")
	case "11":
		cf.WriteString("DLNA.ORG_OP=11;")
	default:
		return "", errors.New("invalid seek flag")
	}

	switch transcode {
	case true:
		cf.WriteString("DLNA.ORG_CI=1;")
	default:
		cf.WriteString("DLNA.ORG_CI=0;")
	}

	cf.WriteString("DLNA.ORG_FLAGS=")
	cf.WriteString(defaultStreamingFlags())

	return cf.String(), nil
}

// GetMimeDetailsFromFile returns the media file mime details.
func GetMimeDetailsFromFile(f io.ReadCloser) (string, error) {
	defer f.Close()
	head := make([]byte, 261)
	_, err := f.Read(head)
	if err != nil {
		return "", fmt.Errorf("getMimeDetailsFromFile error #2: %w", err)
	}

	kind, err := filetype.Match(head)
	if err != nil {
		return "", fmt.Errorf("getMimeDetailsFromFile error #3: %w", err)
	}

	return fmt.Sprintf("%s/%s", kind.MIME.Type, kind.MIME.Subtype), nil
}

// GetMimeDetailsFromStream returns the media URL mime details.
func GetMimeDetailsFromStream(s io.ReadCloser) (string, error) {
	defer s.Close()
	head := make([]byte, 261)
	_, err := s.Read(head)
	if err != nil {
		return "", fmt.Errorf("getMimeDetailsFromStream error: %w", err)
	}

	kind, err := filetype.Match(head)
	if err != nil {
		return "", fmt.Errorf("getMimeDetailsFromStream error  #2: %w", err)
	}

	return fmt.Sprintf("%s/%s", kind.MIME.Type, kind.MIME.Subtype), nil
}
