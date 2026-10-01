//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
)

func TestChromecastStartupReconnectsBeforeReportingFailure(t *testing.T) {
	invalidMedia := errors.New("invalid media")
	stillOffline := errors.New("still offline")
	tests := []struct {
		name             string
		firstError       error
		retryError       error
		stopDuringLoad   bool
		stoppedBefore    bool
		wantReconnect    bool
		wantInitialLoads int
		wantError        error
	}{
		{name: "connection closed", firstError: fmt.Errorf("load: %w", castprotocol.ErrCastDisconnected), wantReconnect: true, wantInitialLoads: 1},
		{name: "socket closed", firstError: net.ErrClosed, wantReconnect: true, wantInitialLoads: 1},
		{name: "connection reset", firstError: fmt.Errorf("load: %w", &net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.ECONNRESET)}), wantReconnect: true, wantInitialLoads: 1},
		{name: "broken pipe", firstError: fmt.Errorf("load: %w", &net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.EPIPE)}), wantReconnect: true, wantInitialLoads: 1},
		{name: "retry also fails", firstError: castprotocol.ErrCastDisconnected, retryError: stillOffline, wantReconnect: true, wantInitialLoads: 1, wantError: stillOffline},
		{name: "stopped during load", firstError: castprotocol.ErrCastDisconnected, stopDuringLoad: true, wantInitialLoads: 1, wantError: castprotocol.ErrCastDisconnected},
		{name: "stopped before load", stoppedBefore: true, wantError: context.Canceled},
		{name: "invalid media", firstError: invalidMedia, wantInitialLoads: 1, wantError: invalidMedia},
		{name: "loaded", wantInitialLoads: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldClient := &castprotocol.CastClient{}
			replacement := &castprotocol.CastClient{}
			screen := &FyneScreen{chromecastActionID: 7, chromecastClient: oldClient}
			if tt.stoppedBefore {
				screen.nextChromecastActionID()
			}
			initialLoads, retryLoads, connects := 0, 0, 0
			got, err := loadChromecastForActionWith(screen, 7, oldClient, castprotocol.LoadRequest{},
				func() (*castprotocol.CastClient, error) {
					connects++
					return replacement, nil
				},
				func(client *castprotocol.CastClient, _ castprotocol.LoadRequest) error {
					switch client {
					case oldClient:
						initialLoads++
						if tt.stopDuringLoad {
							screen.nextChromecastActionID()
						}
						return tt.firstError
					case replacement:
						retryLoads++
						return tt.retryError
					default:
						t.Fatal("load used unexpected client")
						return nil
					}
				},
			)
			if initialLoads != tt.wantInitialLoads {
				t.Fatalf("initial loads = %d, want %d", initialLoads, tt.wantInitialLoads)
			}
			wantRetries := 0
			wantClient := oldClient
			if tt.wantReconnect {
				wantRetries = 1
				wantClient = replacement
			}
			if connects != wantRetries || retryLoads != wantRetries {
				t.Fatalf("connects/retry loads = %d/%d, want %d/%d", connects, retryLoads, wantRetries, wantRetries)
			}
			if got != wantClient || screen.chromecastClient != wantClient {
				t.Fatal("active client was not kept or replaced as expected")
			}
			if !errors.Is(err, tt.wantError) || (tt.wantError == nil && err != nil) {
				t.Fatalf("error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

type blockingStopConn struct {
	cast.Conn
	entered, release, closed chan struct{}
	err                      error
}

func (c *blockingStopConn) Send(_ int, _ cast.Payload, _, _, _ string) error {
	close(c.entered)
	<-c.release
	return c.err
}

func (c *blockingStopConn) Close() error {
	close(c.closed)
	return nil
}

func TestChromecastStopTeardownOrdering(t *testing.T) {
	tt := []struct {
		name string
		wait bool
		err  error
	}{
		{name: "synchronous Stop", wait: true},
		{name: "synchronous Stop failure", wait: true, err: net.ErrClosed},
		{name: "asynchronous Stop"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newMediaCardTestScreen(t)
			s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
			s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()
			client := newConnectedCastClientForTest(t, "http://living-room:8009")
			s.chromecastClient = client
			s.activeDevice = devType{addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast}
			server := httphandlers.NewServer("127.0.0.1:0")
			s.httpserver = server
			started, serverStopped := make(chan error, 1), make(chan struct{})
			go func() {
				server.StartServing(started)
				close(serverStopped)
			}()
			if err := <-started; err != nil {
				t.Fatal(err)
			}
			t.Cleanup(server.StopServer)
			conn := &blockingStopConn{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}), err: tc.err}
			release := sync.OnceFunc(func() { close(conn.release) })
			t.Cleanup(release)
			// Replace the already-closed test client's connection with a Stop
			// that stays in flight, reproducing a slow renderer without hardware.
			app := reflectNewAtField(reflectValueElem(t, client).FieldByName("app")).Elem()
			reflectNewAtField(app.FieldByName("conn")).Set(reflect.ValueOf(conn))
			done := make(chan struct{})
			go func() {
				if tc.wait {
					stopActionSync(s)
				} else {
					stopAction(s)
				}
				close(done)
			}()
			select {
			case <-conn.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("Stop was not sent")
			}
			if tc.wait {
				select {
				case <-done:
					t.Fatal("synchronous Stop returned before teardown")
				case <-time.After(100 * time.Millisecond):
				}
			} else {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("asynchronous Stop blocked on the renderer")
				}
			}
			release()
			for _, completed := range []<-chan struct{}{done, conn.closed, serverStopped} {
				select {
				case <-completed:
				case <-time.After(5 * time.Second):
					t.Fatal("Stop did not close its connection and HTTP server")
				}
			}
		})
	}
}

func TestStopDuringChromecastReconnectStopsMediaServer(t *testing.T) {
	tt := []struct {
		name             string
		connectAfterStop bool
	}{
		{name: "connect cancelled"},
		{name: "connect completes after stop", connectAfterStop: true},
	}
	for _, tt := range tt {
		t.Run(tt.name, func(t *testing.T) {
			app := test.NewApp()
			defer app.Quit()
			client := newConnectedCastClientForTest(t, "http://living-room:8009")
			replacement := newConnectedCastClientForTest(t, "http://living-room:8009")
			t.Cleanup(func() { _ = replacement.Close(false) })
			server := httphandlers.NewServer("127.0.0.1:0")
			t.Cleanup(server.StopServer)
			started := make(chan error, 1)
			stopped := make(chan struct{})
			go func() {
				server.StartServing(started)
				close(stopped)
			}()
			if err := <-started; err != nil {
				t.Fatal(err)
			}
			serverCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			screen := &FyneScreen{
				chromecastActionID: 7,
				chromecastClient:   client,
				activeDevice:       devType{addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast},
				httpserver:         server,
				serverStopCTX:      serverCtx,
				cancelServerStop:   cancel,
				PlayPause:          widget.NewButton("", nil),
				SlideBar:           &tappedSlider{Slider: widget.NewSlider(0, 100)},
				CurrentPos:         binding.NewString(),
				EndPos:             binding.NewString(),
				State:              "Stopped",
			}
			loads := 0
			_, err := loadChromecastForActionWith(screen, 7, client, castprotocol.LoadRequest{},
				func() (*castprotocol.CastClient, error) {
					stopAction(screen)
					if tt.connectAfterStop {
						return replacement, nil
					}
					return nil, context.Canceled
				},
				func(*castprotocol.CastClient, castprotocol.LoadRequest) error {
					loads++
					return net.ErrClosed
				},
			)
			fyne.DoAndWait(func() {})
			if !errors.Is(err, context.Canceled) || loads != 1 {
				t.Fatalf("cancelled reconnect: error = %v, loads = %d", err, loads)
			}
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("Stop left the media server running during reconnect")
			}
			if screen.httpserver != nil || screen.chromecastClient != nil || serverCtx.Err() == nil {
				t.Fatal("Stop retained the cancelled Chromecast session")
			}
			if tt.connectAfterStop && replacement.IsConnected() {
				t.Fatal("cancelled reconnect left the replacement connected")
			}
		})
	}
}
