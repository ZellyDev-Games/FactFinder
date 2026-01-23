package qusb2snes

import (
	"FactFinder/emulator"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const wramBase = 0xF50000
const sramBase = 0xE00000

type MessageReaderWriter interface {
	WriteMessage(data []byte) error
	ReadMessage() (p []byte, err error)
	Connect()
	Connected() bool
}

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
	messageReaderWriter MessageReaderWriter
	attached            bool
}

func NewClient(messageReaderWriter MessageReaderWriter) *Client {
	return &Client{
		messageReaderWriter: messageReaderWriter,
	}
}

func (c *Client) Connect() error {
	c.messageReaderWriter.Connect()
	for {
		if c.messageReaderWriter.Connected() {
			break
		} else {
			time.Sleep(2 * time.Second)
		}
	}

	err := c.SetName("FactFinder")
	if err != nil {
		return err
	}

	devices, err := c.ListDevice()
	if err != nil {
		return err
	}

	err = c.Attach(devices[0])
	if err != nil {
		return err
	}

	c.attached = true
	return nil
}

func (c *Client) Connected() bool {
	return c.messageReaderWriter.Connected()
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
	return c.sendCommand(Attach, CMD, device)
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

func (c *Client) GetValues(specs []emulator.ReadSpec) ([]emulator.Value, error) {
	args := make([]string, 0, len(specs)*2)
	totalSize := 0

	sizes := make([]int, 0, len(specs))
	for _, s := range specs {
		size := s.Size()
		if size <= 0 {
			return nil, fmt.Errorf("invalid size for spec %+v", s)
		}

		// Protocol args: address (hex, upper) + size (hex)
		addr := s.Address + wramBase
		args = append(args, strings.ToUpper(fmt.Sprintf("%x", addr)))
		args = append(args, fmt.Sprintf("%x", size))

		sizes = append(sizes, size)
		totalSize += size
	}
	if err := c.sendCommand(GetAddress, SNES, args...); err != nil {
		return nil, err
	}

	data := make([]byte, 0, totalSize)
	for len(data) < totalSize {
		msgData, err := c.messageReaderWriter.ReadMessage()
		if err != nil {
			return nil, err
		}
		data = append(data, msgData...)
	}

	if len(data) != totalSize {
		return nil, fmt.Errorf("protocol desync: expected %d bytes, got %d", totalSize, len(data))
	}

	out := make([]emulator.Value, 0, len(specs))
	consumed := 0
	for i, s := range specs {
		size := sizes[i]
		b := data[consumed : consumed+size]
		consumed += size

		v, err := decodeValue(s.Type, b)
		if err != nil {
			return nil, fmt.Errorf("decode failed for addr=%#x type=%v: %w", s.Address, s.Type, err)
		}
		out = append(out, v)
	}

	return out, nil
}

func (c *Client) sendCommand(command Command, space Space, args ...string) error {
	query := USB2SnesQuery{
		Opcode:   command.String(),
		Space:    space.String(),
		Flags:    []string{},
		Operands: args,
	}

	jsonData, err := json.Marshal(query)
	if err != nil {
		return err
	}
	err = c.messageReaderWriter.WriteMessage(jsonData)
	return err
}

func (c *Client) getReply() (*USB2SnesResult, error) {
	message, err := c.messageReaderWriter.ReadMessage()
	if err != nil {
		return nil, err
	}
	var result USB2SnesResult
	err = json.Unmarshal(message, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func decodeValue(t emulator.ValueType, b []byte) (emulator.Value, error) {
	switch t {
	case emulator.Bool:
		if len(b) != 1 {
			return emulator.Value{}, errors.New("bool must be 1 byte")
		}
		return emulator.Value{Type: t, Bool: b[0] != 0}, nil

	case emulator.U8:
		if len(b) != 1 {
			return emulator.Value{}, errors.New("u8 must be 1 byte")
		}
		return emulator.Value{Type: t, Unsigned: uint64(b[0])}, nil

	case emulator.I8:
		if len(b) != 1 {
			return emulator.Value{}, errors.New("i8 must be 1 byte")
		}
		return emulator.Value{Type: t, Signed: int64(int8(b[0]))}, nil

	case emulator.U16:
		if len(b) != 2 {
			return emulator.Value{}, errors.New("u16 must be 2 bytes")
		}
		return emulator.Value{Type: t, Unsigned: uint64(binary.LittleEndian.Uint16(b))}, nil

	case emulator.I16:
		if len(b) != 2 {
			return emulator.Value{}, errors.New("i16 must be 2 bytes")
		}
		return emulator.Value{Type: t, Signed: int64(int16(binary.LittleEndian.Uint16(b)))}, nil

	case emulator.U32:
		if len(b) != 4 {
			return emulator.Value{}, errors.New("u32 must be 4 bytes")
		}
		return emulator.Value{Type: t, Unsigned: uint64(binary.LittleEndian.Uint32(b))}, nil

	case emulator.I32:
		if len(b) != 4 {
			return emulator.Value{}, errors.New("i32 must be 4 bytes")
		}
		return emulator.Value{Type: t, Signed: int64(int32(binary.LittleEndian.Uint32(b)))}, nil

	case emulator.U64:
		if len(b) != 8 {
			return emulator.Value{}, errors.New("u64 must be 8 bytes")
		}
		return emulator.Value{Type: t, Unsigned: binary.LittleEndian.Uint64(b)}, nil

	case emulator.I64:
		if len(b) != 8 {
			return emulator.Value{}, errors.New("i64 must be 8 bytes")
		}
		return emulator.Value{Type: t, Signed: int64(binary.LittleEndian.Uint64(b))}, nil

	default:
		return emulator.Value{}, errors.New("unknown type")
	}
}
