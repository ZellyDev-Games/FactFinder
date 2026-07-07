package qusb2snes

import (
	"FactFinder/emulator"
	"FactFinder/logger"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

var log = logger.Module("emulator/qusb2snes/client").SetLevel(logger.InfoLevel)

const wramBase = 0xF50000
const sramBase = 0xE00000

const maxGap = 16
const maxReadSize = 4096

type Command int

const (
	AppVersion Command = iota
	Name
	DeviceList
	Attach
	InfoCommand
	Boot
	Reset
	Menu
	List
	PutFile
	GetFile
	Rename
	Remove
	GetAddress
)

func (c Command) String() string {
	return [...]string{
		"AppVersion",
		"Name",
		"DeviceList",
		"Attach",
		"Info",
		"Boot",
		"Reset",
		"Menu",
		"List",
		"PutFile",
		"GetFile",
		"Rename",
		"Remove",
		"GetAddress",
	}[c]
}

type Space int

const (
	CMD Space = iota
	SNES
)

func (s Space) String() string {
	return [...]string{
		"CMD",
		"SNES",
	}[s]
}

type Info struct {
	Version string
	DevType string
	Game    string
	Flags   []string
}

type USB2SnesQuery struct {
	Opcode   string   `json:"Opcode"`
	Space    string   `json:"Space,omitempty"`
	Flags    []string `json:"Flags,omitempty"`
	Operands []string `json:"Operands"`
}

type USB2SnesResult struct {
	Results []string `json:"Results"`
}

type USB2SnesFileType int

type Client struct {
	m                 sync.Mutex
	conn              *websocket.Conn
	emulatorConnected emulator.ConnectionStatus
	addr              *url.URL
	gameConnected     bool

	respBuf []byte
	byteBuf []byte
}

func NewClient(host, port string) *Client {
	websocketURL := url.URL{
		Scheme: "ws",
		Host:   host + ":" + port,
		Path:   "/",
	}

	log.Info(
		"creating qusb2snes client for %s",
		websocketURL.String(),
	)

	return &Client{
		addr:    &websocketURL,
		respBuf: make([]byte, 4096),
		byteBuf: make([]byte, 0, 16),
	}
}

func (c *Client) ConnectEmulator() emulator.ConnectionStatus {
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic in ConnectEmulator: %v", r)
			c.emulatorConnected = emulator.Disconnected
		}
	}()

	log.Info(
		"attempting websocket connection to %s",
		c.addr.String(),
	)

	conn, _, connErr := websocket.DefaultDialer.Dial(c.addr.String(), nil)
	if connErr != nil {
		log.Error("websocket dial failed: %v", connErr)
		return emulator.Disconnected
	}
	c.conn = conn
	log.Info("connected to usb2snes websocket: %s", c.addr.String())

	version, versionErr := c.AppVersion()
	if versionErr != nil {
		log.Debug("app version request failed: %v", versionErr)
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}
	log.Info(
		"usb2snes app version: %s",
		version,
	)

	err := c.SetName("FactFinder")
	if err != nil {
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	devices, err := c.ListDevice()
	if err != nil {
		log.Error("list devices failed: %v", err)
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	log.Info(
		"found %d devices",
		len(devices),
	)

	for _, d := range devices {
		log.Debug("device: %s", d)
	}

	if len(devices) == 0 {
		log.Warn("no QUSB2SNES devices available")
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	log.Info(
		"attaching to device %s",
		devices[0],
	)
	attachErr := c.Attach(devices[0])
	if attachErr != nil {
		log.Error("attach device failed: %v", attachErr)
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	info, err := c.Info()
	if err == nil {
		log.Info(
			"attached device=%s type=%s game=%s",
			info.Version,
			info.DevType,
			info.Game,
		)
	}

	c.emulatorConnected = emulator.Connected
	return emulator.Connected
}

func (c *Client) Close() error {
	log.Info("closing qusb2snes client")

	c.emulatorConnected = emulator.Disconnected
	c.gameConnected = false

	if c.conn == nil {
		log.Debug("close skipped: no websocket")
		return nil
	}

	err := c.conn.Close()
	if err != nil {
		log.Warn("websocket close failed: %v", err)
		return err
	}

	log.Info("websocket closed")

	return nil
}

func (c *Client) EmulatorConnected() emulator.ConnectionStatus {
	return c.emulatorConnected
}

func (c *Client) GameConnected() bool {
	return c.gameConnected
}

func (c *Client) SetName(name string) error {
	return c.sendCommand(Name, CMD, name)
}

func (c *Client) AppVersion() (string, error) {
	err := c.sendCommand(AppVersion, CMD)
	if err != nil {
		return "", err
	}

	reply, err := c.getReply()
	if err != nil {
		return "", err
	}

	if len(reply.Results) == 0 {
		return "", fmt.Errorf("no results in reply")
	}

	return reply.Results[0], nil
}

func (c *Client) ListDevice() ([]string, error) {
	err := c.sendCommand(DeviceList, CMD)
	if err != nil {
		return nil, err
	}
	reply, err := c.getReply()
	if err != nil {
		return nil, err
	}
	return reply.Results, nil
}

func (c *Client) Attach(device string) error {
	return c.sendCommand(Attach, SNES, device)
}

func (c *Client) Info() (*Info, error) {
	err := c.sendCommand(InfoCommand, CMD)

	if err != nil {
		return nil, err
	}

	usbReply, err := c.getReply()
	if err != nil {
		return nil, err
	}

	info := usbReply.Results

	if len(info) < 3 {
		return nil, fmt.Errorf("unexpected reply length")
	}

	var flags []string

	if len(info) > 3 {
		flags = info[3:]
	}

	return &Info{
		Version: info[0],
		DevType: info[1],
		Game:    info[2],
		Flags:   flags,
	}, nil
}

func (c *Client) Reset() error {
	return c.sendCommand(Reset, CMD)
}

func (c *Client) CompileReadPlan(
	plan *emulator.ReadPlan,
) *emulator.CompiledReadPlan {
	return emulator.CompileReadPlan(
		plan,
		emulator.ResolveAddress,
		qusb2snesAddress,
	)
}

func qusb2snesAddress(
	plan *emulator.ReadPlan,
	spec emulator.ReadSpec,
	addr int,
) int {
	switch spec.Bank {
	case emulator.WRAM:
		return addr + wramBase

	case emulator.SRAM:
		return addr + sramBase
	}

	return addr
}

// generate addresses and sizes
// get data
// copy into external data
func (c *Client) GetValues(plan *emulator.CompiledReadPlan) ([]emulator.Value, error) {
	log.Debug(
		"reading %d merged regions",
		len(plan.Regions),
	)

	var args []string
	var sizes []int
	totalSize := 0

	for index, region := range plan.Regions {
		log.Debug(
			"GetAddress region=%d start=$%X size=%d watches=%d",
			index,
			region.Start,
			region.Size,
			len(region.Watches),
		)

		size := region.Size
		if size <= 0 {
			return nil, fmt.Errorf("invalid size for region")
		}

		// Protocol args: address (hex, upper) + size (hex)
		addr := region.Start
		args = append(args, strings.ToUpper(fmt.Sprintf("%x", addr)))
		args = append(args, fmt.Sprintf("%x", size))

		sizes = append(sizes, size)
		totalSize += size
	}

	log.Debug("requesting SNES memory read: regions=%d totalBytes=%d",
		len(plan.Regions),
		totalSize,
	)

	if err := c.sendCommand(GetAddress, SNES, args...); err != nil {
		return nil, err
	}
	log.Debug(
		"expecting %d bytes total",
		totalSize,
	)

	data := make([]byte, 0, totalSize)
	for len(data) < totalSize {
		_, msgData, err := c.conn.ReadMessage()
		log.Debug(
			"rx binary chunk=%d accumulated=%d/%d",
			len(msgData),
			len(data)+len(msgData),
			totalSize,
		)
		if err != nil {
			log.Error("protocol desync: expected %d got %d", totalSize, len(data))
			return nil, err
		}
		data = append(data, msgData...)
	}

	if len(data) != totalSize {
		log.Error(
			"binary read size mismatch expected=%d got=%d",
			totalSize,
			len(data),
		)
	}

	var out []emulator.Value
	consumed := 0

	for index, mergedRegion := range plan.Regions {
		// size of region
		size := sizes[index]
		// data for region
		b := data[consumed : consumed+size]
		consumed += size

		for _, watch := range mergedRegion.Watches {
			raw := b[watch.Offset : watch.Offset+watch.Size]
			val := emulator.DecodeValue(watch.Spec, raw)
			if val == nil {
				log.Error(
					"decode failed watch=%s type=%v size=%d raw=% X",
					watch.Spec.Name,
					watch.Spec.Type,
					watch.Size,
					raw,
				)
				return nil, fmt.Errorf(
					"unsupported value decode size %d",
					watch.Size,
				)
			}

			out = append(out, *val)
		}
	}
	log.Debug(
		"decoded %d values",
		len(out),
	)

	return out, nil
}

func (c *Client) sendCommand(command Command, space Space, args ...string) error {
	query := USB2SnesQuery{
		Opcode:   command.String(),
		Space:    space.String(),
		Flags:    []string{},
		Operands: args,
	}

	log.Debug("sending command: %s space=%s operands=%v",
		query.Opcode,
		query.Space,
		query.Operands,
	)
	jsonData, err := json.Marshal(query)

	if err != nil {
		log.Error(
			"failed to marshal command %s: %v",
			query.Opcode,
			err,
		)
		return err
	}

	log.Debug(
		"tx opcode=%s space=%s operands=%v",
		query.Opcode,
		query.Space,
		query.Operands,
	)
	err = c.conn.WriteMessage(
		websocket.TextMessage,
		jsonData,
	)

	if err != nil {
		log.Error(
			"websocket write failed opcode=%s: %v",
			query.Opcode,
			err,
		)
		return err
	}

	log.Debug(
		"tx opcode=%s bytes=%d",
		query.Opcode,
		len(jsonData),
	)

	return err
}

func (c *Client) getReply() (*USB2SnesResult, error) {
	_, message, err := c.conn.ReadMessage()

	if err != nil {
		log.Warn(
			"websocket read failed: %v",
			err,
		)
		return nil, err
	}
	log.Debug(
		"rx json bytes=%d",
		len(message),
	)

	var result USB2SnesResult
	err = json.Unmarshal(message, &result)
	log.Debug(
		"rx results=%d",
		len(result.Results),
	)
	if len(result.Results) <= 10 {
		log.Debug(
			"rx payload=%v",
			result.Results,
		)
	}
	if err != nil {
		log.Error(
			"failed to unmarshal usb2snes reply: %v payload=%q",
			err,
			string(message),
		)
		return nil, err
	}

	log.Debug("usb2snes reply: %+v", result)
	return &result, nil
}
