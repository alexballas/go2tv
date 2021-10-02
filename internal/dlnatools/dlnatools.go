package dlnatools

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
)

var (
	// If we're looking to use the dlnaOrgFlagSenderPaced
	// flag for a 32bit build, we need to make sure that we
	// first convert all the flag types to int64
	//dlnaOrgFlagSenderPaced               = 1 << 31
	//dlnaOrgFlagTimeBasedSeek            = 1 << 30
	dlnaOrgFlagByteBasedSeek = 1 << 29
	//dlnaOrgFlagPlayContainer             = 1 << 28
	//dlnaOrgFlagS0Increase                = 1 << 27
	//dlnaOrgFlagSnIncrease                = 1 << 26
	//dlnaOrgFlagRtspPause                 = 1 << 25
	dlnaOrgFlagStreamingTransferMode = 1 << 24
	//dlnaOrgFlagInteractiveTransfertMode = 1 << 23
	dlnaOrgFlagBackgroundTransfertMode = 1 << 22
	dlnaOrgFlagConnectionStall         = 1 << 21
	dlnaOrgFlagDlnaV15                 = 1 << 20

	dlnaprofiles = map[string]string{
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
	return fmt.Sprintf("%.8x%.24x\n", dlnaOrgFlagStreamingTransferMode|
		dlnaOrgFlagBackgroundTransfertMode|
		dlnaOrgFlagConnectionStall|
		dlnaOrgFlagByteBasedSeek|
		dlnaOrgFlagDlnaV15, 0)
}

// BuildContentFeatures - Build the content features string
// for the "contentFeatures.dlna.org" header.
func BuildContentFeatures(file string) string {
	var cf strings.Builder

	ctype := mime.TypeByExtension(filepath.Ext(file))

	dlnaProf, profExists := dlnaprofiles[ctype]
	if profExists {
		cf.WriteString(dlnaProf + ";")
	}

	cf.WriteString("DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=")
	cf.WriteString(defaultStreamingFlags())

	return cf.String()
}
