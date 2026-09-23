//go:build linux && !android

package mpris

import (
	"fmt"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	AppID       = "app.go2tv.go2tv"
	Path        = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	RootIface   = "org.mpris.MediaPlayer2"
	PlayerIface = "org.mpris.MediaPlayer2.Player"
	noTrack     = dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack")
)

// Controls are called on D-Bus worker goroutines. Implementations must not block
// on renderer I/O or update widgets directly.
type Controls struct {
	Play, Pause, Stop, Next, Previous func()
	Seek                              func(positionUS int64)
	SetVolume                         func(volume float64)
	SetLoop                           func(status string)
}

type Snapshot struct {
	Status, Path, Name, Artist, Album, ArtURL, URL   string
	LengthUS, PositionUS                             int64
	CanPlay, CanPause, CanSeek, CanNext, CanPrevious bool
	LoopStatus                                       string
	Volume                                           float64
}

type Service struct {
	conn     *dbus.Conn
	name     string
	props    *prop.Properties
	controls Controls
	mu       sync.Mutex
	snapshot Snapshot
	trackID  dbus.ObjectPath
	sequence uint64
	closed   bool
}

func Start(controls Controls) (*Service, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect session bus: %w", err)
	}
	s := &Service{conn: conn, controls: controls, trackID: noTrack}
	initial := Snapshot{Status: "Stopped", LoopStatus: "None", Volume: 1}
	props, err := prop.Export(conn, Path, s.propertyMap(initial))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("export MPRIS properties: %w", err)
	}
	s.props = props
	if err = conn.Export(&propertiesProxy{props: props, service: s}, Path, "org.freedesktop.DBus.Properties"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("export MPRIS metadata properties: %w", err)
	}
	if err = conn.Export(s, Path, RootIface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("export MPRIS root: %w", err)
	}
	if err = conn.Export(s, Path, PlayerIface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("export MPRIS player: %w", err)
	}
	if err = conn.Export(introspect.NewIntrospectable(s.introspection()), Path, "org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("export MPRIS introspection: %w", err)
	}
	flatpak := os.Getenv("FLATPAK_ID") == AppID
	name := "org.mpris.MediaPlayer2.go2tv"
	if flatpak {
		name = "org.mpris.MediaPlayer2." + AppID
	}
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err == nil && reply != dbus.RequestNameReplyPrimaryOwner {
		name = fmt.Sprintf("%s.instance%d", name, os.Getpid())
		reply, err = conn.RequestName(name, dbus.NameFlagDoNotQueue)
	}
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		if err != nil {
			return nil, fmt.Errorf("request MPRIS bus name %q: %w", name, err)
		}
		return nil, fmt.Errorf("request MPRIS bus name %q: reply %d", name, reply)
	}
	s.snapshot = initial
	s.name = name
	return s, nil
}

func (s *Service) Name() string { return s.name }

func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.conn.Close()
}

func (s *Service) propertyMap(initial Snapshot) prop.Map {
	ro := func(value any) *prop.Prop { return &prop.Prop{Value: value, Emit: prop.EmitTrue} }
	constant := func(value any) *prop.Prop { return &prop.Prop{Value: value, Emit: prop.EmitConst} }
	unsupported := func(*prop.Change) *dbus.Error {
		return dbus.NewError("org.freedesktop.DBus.Error.NotSupported", nil)
	}
	return prop.Map{
		RootIface: {
			"CanQuit": constant(false), "CanRaise": constant(false),
			"CanSetFullscreen": constant(false), "Fullscreen": {Value: false, Writable: true, Emit: prop.EmitConst, Callback: unsupported},
			"HasTrackList": constant(false), "Identity": constant("Go2TV"),
			"DesktopEntry":        constant(AppID),
			"SupportedUriSchemes": constant([]string{}),
			"SupportedMimeTypes":  constant([]string{}),
		},
		PlayerIface: {
			"PlaybackStatus": ro(initial.Status),
			"LoopStatus": {Value: initial.LoopStatus, Writable: true, Emit: prop.EmitTrue, Callback: func(c *prop.Change) *dbus.Error {
				status := c.Value.(string)
				if status != "None" && status != "Track" && status != "Playlist" {
					return prop.ErrInvalidArg
				}
				if s.controls.SetLoop != nil {
					s.controls.SetLoop(status)
				}
				return nil
			}},
			"Rate": {Value: float64(1), Writable: true, Emit: prop.EmitConst, Callback: func(c *prop.Change) *dbus.Error {
				if c.Value.(float64) != 1 {
					return unsupported(c)
				}
				return nil
			}},
			"Shuffle": {Value: false, Writable: true, Emit: prop.EmitConst, Callback: func(c *prop.Change) *dbus.Error {
				if c.Value.(bool) {
					return unsupported(c)
				}
				return nil
			}},
			"Metadata": ro(map[string]dbus.Variant{}),
			"Volume": {Value: initial.Volume, Writable: true, Emit: prop.EmitTrue, Callback: func(c *prop.Change) *dbus.Error {
				if s.controls.SetVolume != nil {
					s.controls.SetVolume(c.Value.(float64))
				}
				return nil
			}},
			"Position":    &prop.Prop{Value: int64(0), Emit: prop.EmitFalse},
			"MinimumRate": constant(float64(1)), "MaximumRate": constant(float64(1)),
			"CanGoNext": ro(false), "CanGoPrevious": ro(false),
			"CanPlay": ro(false), "CanPause": ro(false), "CanSeek": ro(false),
			"CanControl": constant(true),
		},
	}
}

func (s *Service) introspection() *introspect.Node {
	root := introspect.Interface{Name: RootIface, Methods: []introspect.Method{{Name: "Raise"}, {Name: "Quit"}}, Properties: s.props.Introspection(RootIface)}
	player := introspect.Interface{Name: PlayerIface, Properties: s.props.Introspection(PlayerIface), Signals: []introspect.Signal{{Name: "Seeked", Args: []introspect.Arg{{Name: "Position", Type: "x"}}}}}
	for _, name := range []string{"Next", "Previous", "Pause", "PlayPause", "Stop", "Play"} {
		player.Methods = append(player.Methods, introspect.Method{Name: name})
	}
	player.Methods = append(player.Methods,
		introspect.Method{Name: "Seek", Args: []introspect.Arg{{Name: "Offset", Type: "x", Direction: "in"}}},
		introspect.Method{Name: "SetPosition", Args: []introspect.Arg{{Name: "TrackId", Type: "o", Direction: "in"}, {Name: "Position", Type: "x", Direction: "in"}}},
		introspect.Method{Name: "OpenUri", Args: []introspect.Arg{{Name: "Uri", Type: "s", Direction: "in"}}},
	)
	return &introspect.Node{Interfaces: []introspect.Interface{root, player, prop.IntrospectData}}
}

func metadata(v Snapshot, id dbus.ObjectPath) map[string]dbus.Variant {
	if v.Path == "" {
		return map[string]dbus.Variant{}
	}
	m := map[string]dbus.Variant{"mpris:trackid": dbus.MakeVariant(id), "xesam:title": dbus.MakeVariant(v.Name)}
	if v.Artist != "" {
		m["xesam:artist"] = dbus.MakeVariant([]string{v.Artist})
	}
	if v.Album != "" {
		m["xesam:album"] = dbus.MakeVariant(v.Album)
	}
	if v.ArtURL != "" {
		m["mpris:artUrl"] = dbus.MakeVariant(v.ArtURL)
	}
	if v.URL != "" {
		m["xesam:url"] = dbus.MakeVariant(v.URL)
	}
	if v.LengthUS > 0 {
		m["mpris:length"] = dbus.MakeVariant(v.LengthUS)
	}
	return m
}

// godbus/prop merges map updates, retaining fields from the previous track.
// Serve Metadata directly so stopped tracks and absent artwork clear fully.
type propertiesProxy struct {
	props   *prop.Properties
	service *Service
}

func (p *propertiesProxy) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface == PlayerIface && name == "Metadata" {
		v, id := p.service.snapshotCopy()
		return dbus.MakeVariant(metadata(v, id)), nil
	}
	return p.props.Get(iface, name)
}

func (p *propertiesProxy) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	all, err := p.props.GetAll(iface)
	if err != nil {
		return nil, err
	}
	if iface == PlayerIface {
		v, id := p.service.snapshotCopy()
		all["Metadata"] = dbus.MakeVariant(metadata(v, id))
	}
	return all, nil
}

func (p *propertiesProxy) Set(iface, name string, value dbus.Variant) *dbus.Error {
	if iface == PlayerIface && name == "Volume" {
		if volume, ok := value.Value().(float64); ok {
			value = dbus.MakeVariant(min(max(volume, 0), 1))
		}
	}
	if err := p.props.Set(iface, name, value); err != nil {
		return err
	}
	if iface == PlayerIface {
		p.service.mu.Lock()
		switch name {
		case "Volume":
			p.service.snapshot.Volume = value.Value().(float64)
		case "LoopStatus":
			p.service.snapshot.LoopStatus = value.Value().(string)
		}
		p.service.mu.Unlock()
	}
	return nil
}

// Update publishes a consistent player snapshot and change signals.
func (s *Service) Update(v Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if v.Status == "" {
		v.Status = "Stopped"
	}
	if v.LoopStatus == "" {
		v.LoopStatus = "None"
	}
	if v.Path != s.snapshot.Path {
		if v.Path == "" {
			s.trackID = noTrack
		} else {
			s.sequence++
			s.trackID = dbus.ObjectPath(fmt.Sprintf("/go2tv/track/t%d", s.sequence))
		}
	}
	old := s.snapshot
	s.snapshot = v
	s.setChanged("PlaybackStatus", old.Status, v.Status)
	s.setChanged("LoopStatus", old.LoopStatus, v.LoopStatus)
	s.setChanged("Volume", old.Volume, v.Volume)
	s.setChanged("CanPlay", old.CanPlay, v.CanPlay)
	s.setChanged("CanPause", old.CanPause, v.CanPause)
	s.setChanged("CanSeek", old.CanSeek, v.CanSeek)
	s.setChanged("CanGoNext", old.CanNext, v.CanNext)
	s.setChanged("CanGoPrevious", old.CanPrevious, v.CanPrevious)
	if old.Path != v.Path || old.Name != v.Name || old.Artist != v.Artist || old.Album != v.Album || old.ArtURL != v.ArtURL || old.LengthUS != v.LengthUS || old.URL != v.URL {
		_ = s.conn.Emit(Path, "org.freedesktop.DBus.Properties.PropertiesChanged", PlayerIface,
			map[string]dbus.Variant{"Metadata": dbus.MakeVariant(metadata(v, s.trackID))}, []string{})
	}
	s.props.SetMust(PlayerIface, "Position", v.PositionUS)
}

func (s *Service) setChanged(name string, old, value any) {
	if old != value {
		s.props.SetMust(PlayerIface, name, value)
	}
}

func (s *Service) SetPositionUS(position int64, seeked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.snapshot.Path == "" {
		return
	}
	s.snapshot.PositionUS = position
	s.props.SetMust(PlayerIface, "Position", position)
	if seeked {
		_ = s.conn.Emit(Path, PlayerIface+".Seeked", position)
	}
}

func (s *Service) snapshotCopy() (Snapshot, dbus.ObjectPath) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot, s.trackID
}

func (*Service) Raise() *dbus.Error { return nil }
func (*Service) Quit() *dbus.Error  { return nil }
func (s *Service) Next() *dbus.Error {
	v, _ := s.snapshotCopy()
	if v.CanNext && s.controls.Next != nil {
		s.controls.Next()
	}
	return nil
}
func (s *Service) Previous() *dbus.Error {
	v, _ := s.snapshotCopy()
	if v.CanPrevious && s.controls.Previous != nil {
		s.controls.Previous()
	}
	return nil
}
func (s *Service) Pause() *dbus.Error {
	v, _ := s.snapshotCopy()
	if v.Status == "Playing" && v.CanPause && s.controls.Pause != nil {
		s.controls.Pause()
	}
	return nil
}
func (s *Service) PlayPause() *dbus.Error {
	v, _ := s.snapshotCopy()
	if v.Status == "Playing" {
		if v.CanPause && s.controls.Pause != nil {
			s.controls.Pause()
		}
	} else if v.CanPlay && s.controls.Play != nil {
		s.controls.Play()
	}
	return nil
}
func (s *Service) Stop() *dbus.Error {
	v, _ := s.snapshotCopy()
	if v.Status != "Stopped" && s.controls.Stop != nil {
		s.controls.Stop()
	}
	return nil
}
func (s *Service) Play() *dbus.Error {
	v, _ := s.snapshotCopy()
	if v.Status != "Playing" && v.CanPlay && s.controls.Play != nil {
		s.controls.Play()
	}
	return nil
}
func (s *Service) Seek(offset int64) *dbus.Error {
	v, _ := s.snapshotCopy()
	if !v.CanSeek || s.controls.Seek == nil {
		return nil
	}
	position := max(int64(0), v.PositionUS+offset)
	if v.LengthUS > 0 && position > v.LengthUS {
		s.Next()
		return nil
	}
	s.controls.Seek(position)
	return nil
}
func (s *Service) SetPosition(id dbus.ObjectPath, position int64) *dbus.Error {
	v, current := s.snapshotCopy()
	if id == current && v.CanSeek && position >= 0 && position <= v.LengthUS && s.controls.Seek != nil {
		s.controls.Seek(position)
	}
	return nil
}
func (*Service) OpenUri(string) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.NotSupported", nil)
}
