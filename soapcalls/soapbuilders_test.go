package soapcalls

import (
	"testing"
)

func TestSetAVTransportSoapBuild(t *testing.T) {
	tt := []struct {
		name        string
		mediaURL    string
		mediaType   string
		subtitleURL string
		want        string
	}{
		{
			`setAVTransportSoapBuild Test #1`,
			`http://192.168.88.250:3500/video%20%26%20%27example%27.mp4`,
			"video/mp4",
			"http://192.168.88.250:3500/video_example.srt",
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetAVTransportURI xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID><CurrentURI>http://192.168.88.250:3500/video%20%26%20%27example%27.mp4</CurrentURI><CurrentURIMetaData>&lt;DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:sec="http://www.sec.co.kr/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"&gt;&lt;item restricted="false" id="0" parentID="-1"&gt;&lt;sec:CaptionInfo sec:type="srt"&gt;http://192.168.88.250:3500/video_example.srt&lt;/sec:CaptionInfo&gt;&lt;sec:CaptionInfoEx sec:type="srt"&gt;http://192.168.88.250:3500/video_example.srt&lt;/sec:CaptionInfoEx&gt;&lt;upnp:class&gt;object.item.videoItem.movie&lt;/upnp:class&gt;&lt;dc:title&gt;video  &#39;example&#39;.mp4&lt;/dc:title&gt;&lt;res protocolInfo="http-get:*:video/mp4:*"&gt;http://192.168.88.250:3500/video%20%26%20%27example%27.mp4&lt;/res&gt;&lt;res protocolInfo="http-get:*:text/srt:*"&gt;http://192.168.88.250:3500/video_example.srt&lt;/res&gt;&lt;/item&gt;&lt;/DIDL-Lite&gt;</CurrentURIMetaData></u:SetAVTransportURI></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := setAVTransportSoapBuild(tc.mediaURL, tc.mediaType, tc.subtitleURL)
			if err != nil {
				t.Errorf("%s: Failed to call setAVTransportSoapBuild due to %s", tc.name, err.Error())
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
				return
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
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetMute xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel><DesiredMute>1</DesiredMute></u:SetMute></s:Body></s:Envelope>`,
		},
		{
			`setMuteSoapBuild Test #2`,
			"0",
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetMute xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel><DesiredMute>0</DesiredMute></u:SetMute></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := setMuteSoapBuild(tc.input)
			if err != nil {
				t.Errorf("%s: Failed to call setMuteSoapBuild due to %s", tc.name, err.Error())
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
				return
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
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:GetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel></u:GetVolume></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := getVolumeSoapBuild()
			if err != nil {
				t.Errorf("%s: Failed to call setMuteSoapBuild due to %s", tc.name, err.Error())
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
				return
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
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Play xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID><Speed>1</Speed></u:Play></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := playSoapBuild()
			if err != nil {
				t.Errorf("%s: Failed to call playSoapBuild due to %s", tc.name, err.Error())
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
				return
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
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Stop xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID><Speed>1</Speed></u:Stop></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := stopSoapBuild()
			if err != nil {
				t.Errorf("%s: Failed to call stopSoapBuild due to %s", tc.name, err.Error())
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
				return
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
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Pause xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><InstanceID>0</InstanceID><Speed>1</Speed></u:Pause></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := pauseSoapBuild()
			if err != nil {
				t.Errorf("%s: Failed to call pauseSoapBuild due to %s", tc.name, err.Error())
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
				return
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
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:GetMute xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel></u:GetMute></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := getMuteSoapBuild()
			if err != nil {
				t.Errorf("%s: Failed to call getMuteSoapBuild due to %s", tc.name, err.Error())
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
				return
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
			`<?xml version='1.0' encoding='utf-8'?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:SetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><InstanceID>0</InstanceID><Channel>Master</Channel><DesiredVolume>100</DesiredVolume></u:SetVolume></s:Body></s:Envelope>`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := setVolumeSoapBuild(tc.intput)
			if err != nil {
				t.Errorf("%s: Failed to call setVolumeSoapBuild due to %s", tc.name, err.Error())
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
				return
			}
		})
	}
}
