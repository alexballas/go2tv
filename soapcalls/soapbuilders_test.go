package soapcalls

import (
	"strings"
	"testing"

	"go2tv.app/go2tv/v2/utils"
)

func TestSetAVTransportSoapBuild(t *testing.T) {
	tt := []struct {
		name string
		tv   *TVPayload
	}{
		{
			`setAVTransportSoapBuild Test #1`,
			&TVPayload{
				MediaURL:     `http://192.168.88.250:3500/video%20%26%20%27example%27.mp4`,
				MediaType:    "video/mp4",
				SubtitlesURL: "http://192.168.88.250:3500/video_example.srt",
				Transcode:    false,
				Seekable:     true,
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			seekflag := "00"
			if tc.tv.Seekable {
				seekflag = "01"
			}

			contentFeatures, err := utils.BuildContentFeatures(tc.tv.MediaType, seekflag, tc.tv.Transcode)
			if err != nil {
				t.Fatalf("%s: setAVTransportSoapBuild failed to build contentFeatures: %s", tc.name, err.Error())
			}

			want := `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetAVTransportURI xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID><CurrentURI>http://192.168.88.250:3500/video%20%26%20%27example%27.mp4</CurrentURI><CurrentURIMetaData>&lt;DIDL-Lite xmlns=&#34;urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/&#34; xmlns:dc=&#34;http://purl.org/dc/elements/1.1/&#34; xmlns:sec=&#34;http://www.sec.co.kr/&#34; xmlns:upnp=&#34;urn:schemas-upnp-org:metadata-1-0/upnp/&#34;&gt;&lt;item id=&#34;1&#34; parentID=&#34;0&#34; restricted=&#34;1&#34;&gt;&lt;sec:CaptionInfo sec:type=&#34;srt&#34;&gt;http://192.168.88.250:3500/video_example.srt&lt;/sec:CaptionInfo&gt;&lt;sec:CaptionInfoEx sec:type=&#34;srt&#34;&gt;http://192.168.88.250:3500/video_example.srt&lt;/sec:CaptionInfoEx&gt;&lt;dc:title&gt;video &amp;amp; &amp;#39;example&amp;#39;.mp4&lt;/dc:title&gt;&lt;upnp:class&gt;object.item.videoItem.movie&lt;/upnp:class&gt;&lt;res protocolInfo=&#34;http-get:*:video/mp4:` + contentFeatures + `&#34;&gt;http://192.168.88.250:3500/video%20%26%20%27example%27.mp4&lt;/res&gt;&lt;res protocolInfo=&#34;http-get:*:text/srt:*&#34;&gt;http://192.168.88.250:3500/video_example.srt&lt;/res&gt;&lt;/item&gt;&lt;/DIDL-Lite&gt;</CurrentURIMetaData></u:SetAVTransportURI></s:Body></s:Envelope>`

			out, err := setAVTransportSoapBuild(tc.tv)
			if err != nil {
				t.Fatalf("%s: Failed to call setAVTransportSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, want)
			}
		})
	}
}

func TestSetNextAVTransportSoapBuild(t *testing.T) {
	tt := []struct {
		name string
		tv   *TVPayload
	}{
		{
			`setNextAVTransportSoapBuild Test #1`,
			&TVPayload{
				MediaURL:     `http://192.168.88.250:3500/video%20%26%20%27example%27.mp4`,
				MediaType:    "video/mp4",
				SubtitlesURL: "http://192.168.88.250:3500/video_example.srt",
				Transcode:    false,
				Seekable:     true,
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			seekflag := "00"
			if tc.tv.Seekable {
				seekflag = "01"
			}

			contentFeatures, err := utils.BuildContentFeatures(tc.tv.MediaType, seekflag, tc.tv.Transcode)
			if err != nil {
				t.Fatalf("%s: setNextAVTransportSoapBuild failed to build contentFeatures: %s", tc.name, err.Error())
			}

			want := `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetNextAVTransportURI xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID><NextURI>http://192.168.88.250:3500/video%20%26%20%27example%27.mp4</NextURI><NextURIMetaData>&lt;DIDL-Lite xmlns=&#34;urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/&#34; xmlns:dc=&#34;http://purl.org/dc/elements/1.1/&#34; xmlns:sec=&#34;http://www.sec.co.kr/&#34; xmlns:upnp=&#34;urn:schemas-upnp-org:metadata-1-0/upnp/&#34;&gt;&lt;item id=&#34;1&#34; parentID=&#34;0&#34; restricted=&#34;1&#34;&gt;&lt;sec:CaptionInfo sec:type=&#34;srt&#34;&gt;http://192.168.88.250:3500/video_example.srt&lt;/sec:CaptionInfo&gt;&lt;sec:CaptionInfoEx sec:type=&#34;srt&#34;&gt;http://192.168.88.250:3500/video_example.srt&lt;/sec:CaptionInfoEx&gt;&lt;dc:title&gt;video &amp;amp; &amp;#39;example&amp;#39;.mp4&lt;/dc:title&gt;&lt;upnp:class&gt;object.item.videoItem.movie&lt;/upnp:class&gt;&lt;res protocolInfo=&#34;http-get:*:video/mp4:` + contentFeatures + `&#34;&gt;http://192.168.88.250:3500/video%20%26%20%27example%27.mp4&lt;/res&gt;&lt;res protocolInfo=&#34;http-get:*:text/srt:*&#34;&gt;http://192.168.88.250:3500/video_example.srt&lt;/res&gt;&lt;/item&gt;&lt;/DIDL-Lite&gt;</NextURIMetaData></u:SetNextAVTransportURI></s:Body></s:Envelope>`

			out, err := setNextAVTransportSoapBuild(tc.tv, false)
			if err != nil {
				t.Fatalf("%s: Failed to call setNextAVTransportSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, want)
			}
		})
	}
}

func TestSetMuteSoapBuild(t *testing.T) {
	tt := []struct {
		name  string
		input string
		want  string
	}{
		{
			`setMuteSoapBuild Test #1`,
			"1",
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetMute xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel><DesiredMute>1</DesiredMute></u:SetMute></s:Body></s:Envelope>`,
		},
		{
			`setMuteSoapBuild Test #2`,
			"0",
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetMute xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel><DesiredMute>0</DesiredMute></u:SetMute></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := setMuteSoapBuild(tc.input)
			if err != nil {
				t.Fatalf("%s: Failed to call setMuteSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestGetVolumeSoapBuild(t *testing.T) {
	tt := []struct {
		name string
		want string
	}{
		{
			`getVolumeSoapBuild Test #1`,
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:GetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel></u:GetVolume></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := getVolumeSoapBuild()
			if err != nil {
				t.Fatalf("%s: Failed to call setMuteSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestPlaySoapBuild(t *testing.T) {
	tt := []struct {
		name string
		want string
	}{
		{
			`playSoapBuild Test #1`,
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Play xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID><Speed>1</Speed></u:Play></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := playSoapBuild()
			if err != nil {
				t.Fatalf("%s: Failed to call playSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestStopSoapBuild(t *testing.T) {
	tt := []struct {
		name string
		want string
	}{
		{
			`stopSoapBuild Test #1`,
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Stop xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID></u:Stop></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := stopSoapBuild()
			if err != nil {
				t.Fatalf("%s: Failed to call stopSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestPauseSoapBuild(t *testing.T) {
	tt := []struct {
		name string
		want string
	}{
		{
			`pauseSoapBuild Test #1`,
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Pause xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID></u:Pause></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := pauseSoapBuild()
			if err != nil {
				t.Fatalf("%s: Failed to call pauseSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestGetMuteSoapBuild(t *testing.T) {
	tt := []struct {
		name string
		want string
	}{
		{
			`getMuteSoapBuild Test #1`,
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:GetMute xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel></u:GetMute></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := getMuteSoapBuild()
			if err != nil {
				t.Fatalf("%s: Failed to call getMuteSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestSetVolumeSoapBuild(t *testing.T) {
	tt := []struct {
		name   string
		intput string
		want   string
	}{
		{
			`setVolumeSoapBuild Test #1`,
			`100`,
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel><DesiredVolume>100</DesiredVolume></u:SetVolume></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := setVolumeSoapBuild(tc.intput)
			if err != nil {
				t.Fatalf("%s: Failed to call setVolumeSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestGetTransportInfoSoapBuild(t *testing.T) {
	tt := []struct {
		name string
		want string
	}{
		{
			`getTransportInfoSoapBuildTest #1`,
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:GetTransportInfo xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID></u:GetTransportInfo></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := getTransportInfoSoapBuild()
			if err != nil {
				t.Fatalf("%s: Failed to call getTransportInfoSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestGetPositionInfoSoapBuild(t *testing.T) {
	tt := []struct {
		name string
		want string
	}{
		{
			`getPositionInfoSoapBuildTest #1`,
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:GetPositionInfo xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID></u:GetPositionInfo></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := getPositionInfoSoapBuild()
			if err != nil {
				t.Fatalf("%s: Failed to call getPositionInfoSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestSeekSoapBuild(t *testing.T) {
	tt := []struct {
		name   string
		target string
		want   string
	}{
		{
			`seekSoapBuildTest #1`,
			"00:01:30",
			`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Seek xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:01:30</Target></u:Seek></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := seekSoapBuild(tc.target)
			if err != nil {
				t.Fatalf("%s: Failed to call seekSoapBuild due to %s", tc.name, err.Error())
			}
			if string(out) != tc.want {
				t.Fatalf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			}
		})
	}
}

func TestSetAVTransportSoapBuildEscapesAmpersand(t *testing.T) {
	tv := &TVPayload{
		MediaURL:  "http://192.168.88.250:3500/video.mp4?foo=1&bar=2",
		MediaType: "video/mp4",
		Seekable:  true,
	}

	out, err := setAVTransportSoapBuild(tv)
	if err != nil {
		t.Fatalf("setAVTransportSoapBuild failed: %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "video.mp4?foo=1&amp;amp;bar=2") {
		t.Fatalf("expected doubly escaped '&' inside metadata, got %q", got)
	}
}

func TestSetAVTransportSoapBuildEscapesTitleMarkupWithoutStripping(t *testing.T) {
	tv := &TVPayload{
		MediaURL:  "http://192.168.88.250:3500/%3Ctitle%3E%5Cclip.mp4",
		MediaType: "video/mp4",
		Seekable:  true,
	}

	out, err := setAVTransportSoapBuild(tv)
	if err != nil {
		t.Fatalf("setAVTransportSoapBuild failed: %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "&lt;dc:title&gt;&amp;lt;title&amp;gt;\\clip.mp4&lt;/dc:title&gt;") {
		t.Fatalf("expected escaped title markup without stripping, got %q", got)
	}
}
