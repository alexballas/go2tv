package dlnatools

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
)

var (
	DLNA_ORG_FLAG_SENDER_PACED               = 1 << 31
	DLNA_ORG_FLAG_TIME_BASED_SEEK            = 1 << 30
	DLNA_ORG_FLAG_BYTE_BASED_SEEK            = 1 << 29
	DLNA_ORG_FLAG_PLAY_CONTAINER             = 1 << 28
	DLNA_ORG_FLAG_S0_INCREASE                = 1 << 27
	DLNA_ORG_FLAG_SN_INCREASE                = 1 << 26
	DLNA_ORG_FLAG_RTSP_PAUSE                 = 1 << 25
	DLNA_ORG_FLAG_STREAMING_TRANSFER_MODE    = 1 << 24
	DLNA_ORG_FLAG_INTERACTIVE_TRANSFERT_MODE = 1 << 23
	DLNA_ORG_FLAG_BACKGROUND_TRANSFERT_MODE  = 1 << 22
	DLNA_ORG_FLAG_CONNECTION_STALL           = 1 << 21
	DLNA_ORG_FLAG_DLNA_V15                   = 1 << 20

	DLNAprofiles = map[string]string{
		"video/x-mkv":             "DLNA.ORG_PN=MATROSKA",
		"video/x-msvideo":         "DLNA.ORG_PN=AVI",
		"video/mpeg":              "DLNA.ORG_PN=MPEG1",
		"video/vnd.dlna.mpeg-tts": "DLNA.ORG_PN=MPEG1",
		"video/mp4":               "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/quicktime":         "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/x-m4v":             "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/3gpp":              "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5",
		"video/x-flv":             "DLNA.ORG_PN=AVC_MP4_MP_SD_AAC_MULT5"}
)

func defaultStreamingFlags() string {
	return fmt.Sprintf("%.8x%.24x\n", DLNA_ORG_FLAG_STREAMING_TRANSFER_MODE|
		DLNA_ORG_FLAG_BACKGROUND_TRANSFERT_MODE|
		DLNA_ORG_FLAG_CONNECTION_STALL|
		DLNA_ORG_FLAG_BYTE_BASED_SEEK|
		DLNA_ORG_FLAG_DLNA_V15, 0)
}

func BuildContentFeatures(file string) string {
	var cf strings.Builder

	ctype := mime.TypeByExtension(filepath.Ext(file))

	dlna_prof, profExists := DLNAprofiles[ctype]
	if profExists {
		cf.WriteString(dlna_prof + ";")
	}

	cf.WriteString("DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=")
	cf.WriteString(defaultStreamingFlags())

	return cf.String()
}
