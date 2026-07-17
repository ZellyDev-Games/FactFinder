package qusb2snes

import (
	"FactFinder/logger"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var wsLog = logger.Module("emulator/qusb2snes/websocket").SetLevel(logger.InfoLevel)

var ErrClosed = errors.New("client is closed")

const RetryWait = time.Second * 3

// WebsocketClient wraps a *websocket.Conn with some state and provides some retry logic
// that we attempt to hide from the caller
type WebsocketClient struct {
	m           sync.Mutex
	url         url.URL
	conn        *websocket.Conn
	connected   bool
	reconnectCh chan struct{}
	doneCh      chan struct{}
	closeOnce   sync.Once
	startOnce   sync.Once
}

// NewWebsocketClient returns an unconnected client with a preset URL
func NewWebsocketClient(url url.URL) *WebsocketClient {
	return &WebsocketClient{
		url:         url,
		reconnectCh: make(chan struct{}, 1),
		doneCh:      make(chan struct{}),
	}
}

// Connected returns the connected state of the WebsocketClient
func (w *WebsocketClient) Connected() bool {
	w.m.Lock()
	defer w.m.Unlock()
	return w.connected
}

func (w *WebsocketClient) WriteMessage(data []byte) error {
	w.m.Lock()
	if !w.connected || w.conn == nil {
		w.m.Unlock()
		return errors.New("WriteMessage called on disconnected client")
	}
	conn := w.conn
	w.m.Unlock()

	err := conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		w.signalReconnect()
		wsLog.Warn("websocket write failed, triggering reconnect: %v", err)
		return err
	}

	return nil
}

func (w *WebsocketClient) ReadMessage() (p []byte, err error) {
	w.m.Lock()
	if !w.connected || w.conn == nil {
		w.m.Unlock()
		return []byte{}, errors.New("ReadMessage called on disconnected client")
	}
	conn := w.conn
	w.m.Unlock()

	_, message, err := conn.ReadMessage()
	if err != nil {
		w.signalReconnect()
		wsLog.Warn("websocket read failed, triggering reconnect")
	}
	return message, err
}

// Connect attempts to establish a websocket connection to the configured URL
// and manages the state, and retry logic in a goroutine
func (w *WebsocketClient) Connect() {
	w.startOnce.Do(func() {
		go func() {
			for {
				conn, err := w.safeConnect()
				if err != nil || conn == nil {
					// closed branch (i.e. doneCh hs been closed, most likely via Close())
					// there is nothing left to do, this client can never be used again
					if errors.Is(err, ErrClosed) {
						return
					}

					// retry branch (i.e. a transient network error occurred, and we might be able to reconnect
					if conn == nil && err == nil {
						wsLog.Debug("websocket reconnecting in %v", RetryWait)
					} else if err != nil {
						wsLog.Error("websocket connect failed: %v", err)
					}

					timer := time.NewTimer(RetryWait)
					select {
					case <-w.doneCh:
						// closed during retry.  Same as Closed branch: Caller should never expect this client
						// to be useful again
						timer.Stop()
						wsLog.Info("websocket client closed")
						return
					case <-timer.C:
						continue
					}
				}

				// everything worked properly, lock and set state.
				w.m.Lock()
				w.conn = conn
				w.connected = true
				wsLog.Debug("websocket state -> connected")
				w.m.Unlock()

				// wait for an explicit CLose() or a read/write error that triggers a reconnect attempt
				select {
				case <-w.doneCh:
					wsLog.Info("websocket Close() called")
					w.closeConnection()
					return
				case <-w.reconnectCh:
					w.closeConnection()
				}
			}
		}()
	})
}

// Close cleanly shuts down this client
// Callers should no longer expect this client to be useful
func (w *WebsocketClient) Close() {
	w.closeOnce.Do(func() {
		close(w.doneCh)
	})
}

// signalReconnect is a helper function to send a non-blocking message to the reconnectCh
func (w *WebsocketClient) signalReconnect() {
	wsLog.Debug("reconnect signal triggered")
	select {
	case w.reconnectCh <- struct{}{}:
	default:
	}
}

// safeConnect checks the closed channel before attempting to connect
func (w *WebsocketClient) safeConnect() (*websocket.Conn, error) {
	select {
	case <-w.doneCh:
		return nil, ErrClosed
	default:
	}
	conn, _, err := websocket.DefaultDialer.Dial(w.url.String(), nil)
	if err != nil {
		wsLog.Warn("dial failed: %v", err)
	} else {
		wsLog.Info("websocket connected: %s", w.url.String())
	}
	return conn, err
}

// closeConnection safely captures the underlying connection, clears the state of the WebsocketClient
// then attempts to silently close the underlying websocket.Conn
func (w *WebsocketClient) closeConnection() {
	wsLog.Debug("closing websocket connection (state reset)")
	w.m.Lock()
	c := w.conn
	w.conn = nil
	w.connected = false
	w.m.Unlock()

	if c != nil {
		_ = c.Close()
		wsLog.Info("websocket connection closed")
	}
}
