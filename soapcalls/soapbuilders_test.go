package soapcalls

import (
	"strings"
	"testing"

	"go2tv.app/go2tv/v2/metadata"
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

func TestBuildDIDLLiteMetadataExact(t *testing.T) {
	tv := &TVPayload{
		MediaURL:     "http://host/track.mp3",
		MediaType:    "audio/mpeg",
		SubtitlesURL: "http://host/track.srt",
		Seekable:     true,
	}
	mediaMetadata := metadata.Media{
		Title:  "Title & One",
		Artist: "Artist <One>",
		Album:  "Album",
		Artwork: &metadata.Artwork{
			URL: "http://host/artwork/hash.jpg?x=1&y=2",
		},
	}

	contentFeatures, err := utils.BuildContentFeatures(tv.MediaType, "01", false)
	if err != nil {
		t.Fatalf("BuildContentFeatures() error = %v", err)
	}
	want := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:sec="http://www.sec.co.kr/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item id="1" parentID="0" restricted="1"><sec:CaptionInfo sec:type="srt">http://host/track.srt</sec:CaptionInfo><sec:CaptionInfoEx sec:type="srt">http://host/track.srt</sec:CaptionInfoEx><dc:title>Title &amp; One</dc:title><upnp:artist>Artist &lt;One&gt;</upnp:artist><upnp:album>Album</upnp:album><upnp:class>object.item.audioItem.musicTrack</upnp:class><upnp:albumArtURI>http://host/artwork/hash.jpg?x=1&amp;y=2</upnp:albumArtURI><res protocolInfo="http-get:*:audio/mpeg:` + contentFeatures + `">http://host/track.mp3</res><res protocolInfo="http-get:*:text/srt:*">http://host/track.srt</res></item></DIDL-Lite>`

	got, err := buildDIDLLite(tv, tv.MediaURL, mediaMetadata)
	if err != nil {
		t.Fatalf("buildDIDLLite() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("DIDL = %s, want %s", got, want)
	}
}

func TestBuildDIDLLiteTranscodedKeepsPlayableResourceContract(t *testing.T) {
	tv := &TVPayload{
		MediaURL: "http://host/movie.mp4", MediaType: "video/mp4",
		Transcode: true, MediaDuration: 6176.17,
	}
	got, err := buildDIDLLite(tv, tv.MediaURL, metadata.Media{Title: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	metadata := string(got)
	for _, want := range []string{"http-get:*:video/mpeg:DLNA.ORG_CI=1", "DLNA.ORG_CI=1", `duration="01:42:56"`, "http://host/movie.mp4"} {
		if !strings.Contains(metadata, want) {
			t.Fatalf("DIDL missing %q: %s", want, metadata)
		}
	}
	if strings.Contains(metadata, "DLNA.ORG_OP=10") {
		t.Fatalf("DIDL advertises unsupported live time seek: %s", metadata)
	}
}

func TestSetNextAVTransportArtworkAndClear(t *testing.T) {
	tv := &TVPayload{
		MediaURL:  "http://host/track.mp3",
		MediaType: "audio/mpeg",
		Metadata: metadata.Media{
			Title:   "Track",
			Artwork: &metadata.Artwork{URL: "http://host/artwork/hash.jpg"},
		},
	}

	withArtwork, err := setNextAVTransportSoapBuild(tv, false)
	if err != nil {
		t.Fatalf("setNextAVTransportSoapBuild(false) error = %v", err)
	}
	if !strings.Contains(string(withArtwork), `&lt;upnp:albumArtURI&gt;http://host/artwork/hash.jpg&lt;/upnp:albumArtURI&gt;`) {
		t.Fatalf("next metadata missing artwork: %s", withArtwork)
	}

	cleared, err := setNextAVTransportSoapBuild(tv, true)
	if err != nil {
		t.Fatalf("setNextAVTransportSoapBuild(true) error = %v", err)
	}
	if strings.Contains(string(cleared), "albumArtURI") {
		t.Fatalf("cleared next metadata retained artwork: %s", cleared)
	}
}

func TestSetAVTransportArtworkEscaping(t *testing.T) {
	tv := &TVPayload{
		MediaURL:  "http://host/track.mp3",
		MediaType: "audio/mpeg",
		Metadata: metadata.Media{
			Artwork: &metadata.Artwork{URL: "http://host/artwork/hash.jpg?x=1&y=2"},
		},
	}

	standard, err := setAVTransportSoapBuild(tv)
	if err != nil {
		t.Fatalf("setAVTransportSoapBuild() error = %v", err)
	}
	standardArtwork := `&lt;upnp:albumArtURI&gt;http://host/artwork/hash.jpg?x=1&amp;amp;y=2&lt;/upnp:albumArtURI&gt;`
	if !strings.Contains(string(standard), standardArtwork) {
		t.Fatalf("standard metadata artwork escaping = %s", standard)
	}

	compat, err := setAVTransportSoapBuildWithCompat(tv, true)
	if err != nil {
		t.Fatalf("setAVTransportSoapBuildWithCompat() error = %v", err)
	}
	compatArtwork := `&lt;upnp:albumArtURI&gt;http://host/artwork/hash.jpg?x=1&amp;y=2&lt;/upnp:albumArtURI&gt;`
	if !strings.Contains(string(compat), compatArtwork) {
		t.Fatalf("compat metadata artwork escaping = %s", compat)
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

func TestSetAVTransportSoapBuildEscapesTitleMarkup(t *testing.T) {
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

func TestSetAVTransportSoapBuildLegacyCompat(t *testing.T) {
	tv := &TVPayload{
		MediaURL:  `http://192.168.88.250:3500/video%20%26%20%27example%27.mp4`,
		MediaType: "video/mp4",
		Seekable:  true,
	}

	out, err := setAVTransportSoapBuildWithCompat(tv, true)
	if err != nil {
		t.Fatalf("setAVTransportSoapBuildWithCompat failed: %v", err)
	}

	got := string(out)
	if !strings.Contains(got, `CurrentURIMetaData>&lt;DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"`) {
		t.Fatalf("expected compat metadata to unescape XML quotes, got %q", got)
	}

	if strings.Contains(got, "xmlns=&#34;") {
		t.Fatalf("expected compat metadata to remove encoded quotes, got %q", got)
	}
}

func TestSetAVTransportSoapBuildLegacyCompatEscapesCurrentURI(t *testing.T) {
	tv := &TVPayload{
		MediaURL:  "http://192.168.88.250:3500/video.mp4?foo=1&bar=2",
		MediaType: "video/mp4",
		Seekable:  true,
	}

	out, err := setAVTransportSoapBuildWithCompat(tv, true)
	if err != nil {
		t.Fatalf("setAVTransportSoapBuildWithCompat failed: %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "<CurrentURI>http://192.168.88.250:3500/video.mp4?foo=1&amp;bar=2</CurrentURI>") {
		t.Fatalf("expected CurrentURI to stay XML escaped, got %q", got)
	}
}
