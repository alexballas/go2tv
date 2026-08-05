package gui

import (
	"errors"
	"math"

	"github.com/alexballas/refyne/v2"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

func chromecastProgressTimeline(mediaDuration float64, ffmpegSeek int, rendererDuration, rendererCurrent float32) (float64, float64) {
	duration := float64(rendererDuration)
	current := float64(rendererCurrent)
	if mediaDuration > 0 {
		duration = mediaDuration
		current += float64(ffmpegSeek)
	}
	return current, duration
}

func dlnaSeekTimeline(tvdata *soapcalls.TVPayload) (int, string, error) {
	if tvdata == nil {
		return 0, "", errors.New("DLNA media session unavailable")
	}
	if tvdata.Transcode && tvdata.MediaDuration > 0 {
		total := int(math.Round(tvdata.MediaDuration))
		return total, utils.SecondsToClockTime(total), nil
	}

	position, err := tvdata.GetPositionInfo()
	if err != nil {
		return 0, "", err
	}
	if len(position) == 0 {
		return 0, "", errors.New("DLNA renderer returned no duration")
	}
	total, err := utils.ClockTimeToSeconds(position[0])
	if err != nil {
		return 0, "", err
	}
	end, err := utils.FormatClockTime(position[0])
	if err != nil {
		return 0, "", err
	}
	return total, end, nil
}

func dlnaProgressTimeline(tvdata *soapcalls.TVPayload, position []string) (int, int, string, error) {
	if tvdata == nil {
		return 0, 0, "", errors.New("DLNA media session unavailable")
	}
	if len(position) < 2 {
		return 0, 0, "", errors.New("DLNA renderer returned incomplete position")
	}

	current, err := utils.ClockTimeToSeconds(position[1])
	if err != nil {
		return 0, 0, "", err
	}

	total := 0
	if tvdata.Transcode && tvdata.MediaDuration > 0 {
		total = int(math.Round(tvdata.MediaDuration))
	} else {
		total, err = utils.ClockTimeToSeconds(position[0])
		if err != nil {
			return 0, 0, "", err
		}
	}
	if tvdata.Transcode {
		current += tvdata.FFmpegSeek
	}
	if total <= 0 {
		return 0, 0, "", errors.New("DLNA media duration unavailable")
	}
	current = min(max(current, 0), total)

	return current, total, utils.SecondsToClockTime(total), nil
}

func showDLNATranscodeTimeline(screen *FyneScreen, tvdata *soapcalls.TVPayload) {
	if screen == nil || tvdata == nil || !tvdata.Transcode || tvdata.MediaDuration <= 0 {
		return
	}

	total := int(math.Round(tvdata.MediaDuration))
	if total <= 0 {
		return
	}
	current := min(max(tvdata.FFmpegSeek, 0), total)
	value := float64(current) * screen.SlideBar.Max / float64(total)
	fyne.Do(func() {
		screen.SlideBar.SetValue(value)
		screen.CurrentPos.Set(utils.SecondsToClockTime(current))
		screen.EndPos.Set(utils.SecondsToClockTime(total))
	})
}
