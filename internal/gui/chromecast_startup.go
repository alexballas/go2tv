package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"go2tv.app/go2tv/v2/castprotocol"
)

const chromecastConnectAttempts = 2

func (screen *FyneScreen) shouldCloseChromecastClientOnSelectionChange() bool {
	if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
		return false
	}
	// Active device is set before loading; keep that connection during startup.
	return screen.chromecastSessionClient() == nil
}

// Stop must retain session ownership while reconnect has closed the old client.
func (screen *FyneScreen) chromecastClientForStop() *castprotocol.CastClient {
	screen.mu.RLock()
	client, device := screen.chromecastClient, screen.activeDevice
	screen.mu.RUnlock()
	if !chromecastClientOwnsDevice(client, device) {
		return nil
	}
	return client
}

func (screen *FyneScreen) installChromecastClientForAction(actionID uint64, client *castprotocol.CastClient) bool {
	screen.mu.Lock()
	defer screen.mu.Unlock()
	if screen.chromecastActionID != actionID {
		return false
	}
	screen.chromecastClient = client
	return true
}

func connectChromecastForAction(screen *FyneScreen, actionID uint64, device devType) (*castprotocol.CastClient, error) {
	ctx := screen.playbackStartupContext()
	var lastErr error
	for attempt := range chromecastConnectAttempts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !screen.isChromecastActionCurrent(actionID) {
			return nil, context.Canceled
		}
		client, err := castprotocol.NewCastClient(device.addr)
		if err != nil {
			return nil, fmt.Errorf("chromecast init: %w", err)
		}
		client.LogOutput = screen.Debug
		if err := client.ConnectContext(ctx); err == nil {
			if !screen.isChromecastActionCurrent(actionID) {
				_ = client.Close(false)
				return nil, context.Canceled
			}
			return client, nil
		} else {
			lastErr = err
		}
		_ = client.ForceClose()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt+1 < chromecastConnectAttempts {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("chromecast connect: %w", lastErr)
}

func recoverableChromecastLoadError(err error) bool {
	if errors.Is(err, castprotocol.ErrCastDisconnected) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Socket reads/writes also fail with non-timeout errors such as connection
	// reset and broken pipe. Match the operation across platforms rather than
	// relying on platform-specific errno values.
	var opErr *net.OpError
	if errors.As(err, &opErr) && (opErr.Op == "read" || opErr.Op == "write") {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func retryChromecastStartupLoad(load func() error, actionCurrent func() bool, reconnectAndLoad func() error) error {
	err := load()
	if !recoverableChromecastLoadError(err) || !actionCurrent() {
		return err
	}
	return reconnectAndLoad()
}

func loadChromecastForAction(screen *FyneScreen, actionID uint64, device devType, client *castprotocol.CastClient, req castprotocol.LoadRequest) (*castprotocol.CastClient, error) {
	return loadChromecastForActionWith(screen, actionID, client, req,
		func() (*castprotocol.CastClient, error) {
			return connectChromecastForAction(screen, actionID, device)
		},
		func(client *castprotocol.CastClient, req castprotocol.LoadRequest) error {
			return client.LoadMediaContext(screen.playbackStartupContext(), req)
		},
	)
}

func loadChromecastForActionWith(
	screen *FyneScreen,
	actionID uint64,
	client *castprotocol.CastClient,
	req castprotocol.LoadRequest,
	connect func() (*castprotocol.CastClient, error),
	load func(*castprotocol.CastClient, castprotocol.LoadRequest) error,
) (*castprotocol.CastClient, error) {
	current := func() bool { return screen.isChromecastActionCurrent(actionID) }
	if !current() {
		return client, context.Canceled
	}
	loadCurrent := func(client *castprotocol.CastClient) error {
		ctx := screen.playbackStartupContext()
		if err := ctx.Err(); err != nil {
			return err
		}
		closed := make(chan struct{})
		cancelLoad := context.AfterFunc(ctx, func() {
			defer close(closed)
			client.StopAndClose(50 * time.Millisecond)
		})
		err := load(client, req)
		if !cancelLoad() {
			<-closed
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	err := retryChromecastStartupLoad(
		func() error { return loadCurrent(client) },
		current,
		func() error {
			client.Log().Warn("cast connection lost during load; reconnecting", "Method", "LoadMedia")
			if client.IsConnected() && current() {
				_ = client.ForceClose()
			}
			replacement, err := connect()
			if err != nil {
				return fmt.Errorf("chromecast reconnect: %w", err)
			}
			if !screen.installChromecastClientForAction(actionID, replacement) {
				_ = replacement.Close(false)
				return context.Canceled
			}
			client = replacement
			return loadCurrent(client)
		},
	)
	return client, err
}
