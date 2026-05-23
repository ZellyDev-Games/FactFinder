package qusb2snes

import (
	"FactFinder/emulator"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/bits"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

const wramBase = 0xF50000
const sramBase = 0xE00000

const maxGap = 16
const maxReadSize = 4096

// type MessageReaderWriter interface {
// 	WriteMessage(data []byte) error
// 	ReadMessage() (p []byte, err error)
// 	Connect()
// 	Connected() bool
// }

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
	// messageReaderWriter MessageReaderWriter
	// attached            bool
	m                 sync.Mutex
	conn              *websocket.Conn
	emulatorConnected emulator.ConnectionStatus
	addr              *url.URL
	gameConnected     bool

	respBuf []byte
	byteBuf []byte
}

func NewClient(host, port string) *Client {
	// address := fmt.Sprintf("%s:%s", ip, port)
	websocketURL := url.URL{Scheme: "ws", Host: host + ":" + port, Path: "/"}
	// if err != nil {
	// return nil, fmt.Errorf("can't resolve address: %w", err)
	// }

	return &Client{
		addr:    &websocketURL,
		respBuf: make([]byte, 4096),
		byteBuf: make([]byte, 0, 16),
	}
}

func (c *Client) ConnectEmulator() emulator.ConnectionStatus {
	conn, _, err := websocket.DefaultDialer.Dial(c.addr.String(), nil)
	if err != nil {
		fmt.Println(err)
		return emulator.Disconnected
	}
	c.conn = conn

	for {
		_, err = c.AppVersion()
		if err != nil {
			fmt.Println(err)
			continue
		}
		err := c.SetName("FactFinder")
		if err != nil {
			continue
		}

		devices, err := c.ListDevice()
		if err != nil {
			continue
		}

		err = c.Attach(devices[0])
		if err != nil {
			break
		}
	}

	c.emulatorConnected = emulator.Connected
	return emulator.Connected
}

// func (c *Client) Connect() error {
// 	c.messageReaderWriter.Connect()
// 	for {
// 		if c.messageReaderWriter.Connected() {
// 			break
// 		} else {
// 			time.Sleep(2 * time.Second)
// 		}
// 	}

// 	err := c.SetName("FactFinder")
// 	if err != nil {
// 		return err
// 	}

// 	devices, err := c.ListDevice()
// 	if err != nil {
// 		return err
// 	}

// 	err = c.Attach(devices[0])
// 	if err != nil {
// 		return err
// 	}

// 	c.attached = true
// 	return nil
// }

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

func (c *Client) CompileReadPlan(plan *emulator.ReadPlan) *emulator.CompiledReadPlan {
	type tempWatch struct {
		Spec emulator.ReadSpec
		Addr int
		Size int
	}

	tmp := make([]tempWatch, 0, len(plan.Watches))

	for _, spec := range plan.Watches {
		addr := resolveAddress( /*plan,*/ spec)

		size := spec.SizeOverride
		if size == 0 {
			size = spec.Size()
		}

		tmp = append(tmp, tempWatch{
			Spec: spec,
			Addr: addr,
			Size: size,
		})
	}

	slices.SortFunc(tmp, func(a, b tempWatch) int {
		return a.Addr - b.Addr
	})

	out := &emulator.CompiledReadPlan{}

	for _, w := range tmp {
		if len(out.Regions) == 0 {
			out.Regions = append(out.Regions, emulator.MergedRegion{
				Bank:  w.Spec.Bank,
				Start: w.Addr,
				Size:  w.Size,
			})
		}

		cur := &out.Regions[len(out.Regions)-1]

		curEnd := cur.Start + cur.Size
		wEnd := w.Addr + w.Size

		canMerge :=
			w.Addr <= curEnd+maxGap &&
				(wEnd-cur.Start) <= maxReadSize

		if !canMerge {
			out.Regions = append(out.Regions, emulator.MergedRegion{
				Bank:  w.Spec.Bank,
				Start: w.Addr,
				Size:  w.Size,
			})

			cur = &out.Regions[len(out.Regions)-1]
		} else {
			if wEnd > curEnd {
				cur.Size = wEnd - cur.Start
			}
		}

		cur.Watches = append(cur.Watches, emulator.ResolvedWatch{
			Spec:   w.Spec,
			Addr:   w.Addr,
			Size:   w.Size,
			Offset: w.Addr - cur.Start,
		})
	}

	for i := range out.Regions {
		out.Regions[i].Buffer = make([]byte, out.Regions[i].Size)
	}

	return out
}

func resolveAddress( /*plan *emulator.ReadPlan,*/ spec emulator.ReadSpec) int {
	switch spec.Bank {
	case emulator.WRAM:
		// WRAM  Bank = "wram"  // SNES/GB/GBC Memory
		return int(spec.Address)

	case emulator.SRAM:
		// SRAM  Bank = "sram"  // SNES Save Memory
		// if plan.HiROM {
		// return 0x300000 +
		// 0x6000 +
		// (int(spec.Address) % 0xA000) +
		// (int(spec.Address)/0xA000)*0x10000
		// }
		//
		// return 0x700000 +
		// (int(spec.Address) % 0x8000) +
		// (int(spec.Address)/0x8000)*0x10000
		return int(spec.Address)
	case emulator.RAM:
		// RAM   Bank = "ram"   // PSX/NES/Genesis Memory
		return int(spec.Address)
	case emulator.IWRAM:
		// IWRAM Bank = "iwram" // GBA Internal Memory
		return int(spec.Address)
	case emulator.EWRAM:
		// EWRAM Bank = "ewram" // GBA External Memory
		return int(spec.Address)
	case emulator.FCRAM:
		// FCRAM Bank = "fcram" // 3DS Memory
		return int(spec.Address)
	case emulator.PSRAM:
		// PSRAM Bank = "psram" // DS Memory
		return int(spec.Address)
	case emulator.RDRAM:
		// RDRAM Bank = "rdram" // N64 Memory
		return int(spec.Address)
	}

	return 0
}

// generate addresses and sizes
// get data
// copy into external data
func (c *Client) GetValues(plan *emulator.CompiledReadPlan) ([]emulator.Value, error) {
	// vals := make([]emulator.Value, 0)

	// args := make([]string, 0, len(specs)*2)
	var args []string
	// sizes := make([]int, 0, len(specs))
	var sizes []int
	totalSize := 0

	for _, region := range plan.Regions {
		size := region.Size
		if size <= 0 {
			return nil, fmt.Errorf("invalid size for region")
		}

		// Protocol args: address (hex, upper) + size (hex)
		addr := region.Start + wramBase
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
		_, msgData, err := c.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		data = append(data, msgData...)
	}

	if len(data) != totalSize {
		return nil, fmt.Errorf("protocol desync: expected %d bytes, got %d", totalSize, len(data))
	}

	// out := make([]emulator.Value, 0, len(specs))
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
			v := decodeValue(watch.Spec, raw)
			if v == nil {
				return nil, fmt.Errorf(
					"unsupported value decode size %d",
					watch.Size,
				)
			}

			out = append(out, *v)
		}
		// if err != nil {
		// return nil, fmt.Errorf("decode failed for addr=%#x type=%v: %w", s.Address, s.Type, err)
		// }
		// out = append(out, *v)
	}
	return out, nil
}

// func (c *Client) GetValues(specs []emulator.ReadSpec) ([]emulator.Value, error) {
// 	args := make([]string, 0, len(specs)*2)
// 	totalSize := 0

// 	sizes := make([]int, 0, len(specs))
// 	for _, s := range specs {
// 		size := s.Size()
// 		if size <= 0 {
// 			return nil, fmt.Errorf("invalid size for spec %+v", s)
// 		}

// 		// Protocol args: address (hex, upper) + size (hex)
// 		addr := s.Address + wramBase
// 		args = append(args, strings.ToUpper(fmt.Sprintf("%x", addr)))
// 		args = append(args, fmt.Sprintf("%x", size))

// 		sizes = append(sizes, size)
// 		totalSize += size
// 	}
// 	if err := c.sendCommand(GetAddress, SNES, args...); err != nil {
// 		return nil, err
// 	}

// 	data := make([]byte, 0, totalSize)
// 	for len(data) < totalSize {
// 		msgData, err := c.messageReaderWriter.ReadMessage()
// 		if err != nil {
// 			return nil, err
// 		}
// 		data = append(data, msgData...)
// 	}

// 	if len(data) != totalSize {
// 		return nil, fmt.Errorf("protocol desync: expected %d bytes, got %d", totalSize, len(data))
// 	}

// 	out := make([]emulator.Value, 0, len(specs))
// 	consumed := 0
// 	for i, s := range specs {
// 		size := sizes[i]
// 		b := data[consumed : consumed+size]
// 		consumed += size

// 		v, err := decodeValue(s.Type, b)
// 		if err != nil {
// 			return nil, fmt.Errorf("decode failed for addr=%#x type=%v: %w", s.Address, s.Type, err)
// 		}
// 		out = append(out, v)
// 	}

// 	return out, nil
// }

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
	err = c.conn.WriteMessage(websocket.TextMessage, jsonData)
	return err
}

func (c *Client) getReply() (*USB2SnesResult, error) {
	_, message, err := c.conn.ReadMessage()
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

func decodeValue(readSpec emulator.ReadSpec, raw []byte) *emulator.Value {
	val := emulator.Value{Type: readSpec.Type, Name: readSpec.Name}

	need := readSpec.Size()
	var u uint64

	switch need {
	case 1:
		u = uint64(raw[0])
	case 2:
		u = uint64(binary.LittleEndian.Uint16(raw))
	case 4:
		u = uint64(binary.LittleEndian.Uint32(raw))
	case 8:
		u = binary.LittleEndian.Uint64(raw)
	default:
		return nil
	}

	switch readSpec.Type {
	case emulator.I8:
		val.Signed = int64(int8(raw[0]))
	case emulator.I16:
		val.Signed = int64(int16(binary.LittleEndian.Uint16(raw)))
	case emulator.I32:
		val.Signed = int64(int32(binary.LittleEndian.Uint32(raw)))
	case emulator.I64:
		val.Signed = int64(binary.LittleEndian.Uint64(raw))
	case emulator.U8, emulator.U16, emulator.U32, emulator.U64:
		val.Unsigned = u
	case emulator.Bool:
		val.Bool = u != 0
	case emulator.FlagCount:
		if readSpec.Mask != 0 {
			u = u & 0x3FFF
		}
		val.FlagCount = bits.OnesCount64(u)
	}

	return &val
}

// func decodeValue(t emulator.ValueType, b []byte) (emulator.Value, error) {
// 	switch t {
// 	case emulator.Bool:
// 		if len(b) != 1 {
// 			return emulator.Value{}, errors.New("bool must be 1 byte")
// 		}
// 		return emulator.Value{Type: t, Bool: b[0] != 0}, nil

// 	case emulator.U8:
// 		if len(b) != 1 {
// 			return emulator.Value{}, errors.New("u8 must be 1 byte")
// 		}
// 		return emulator.Value{Type: t, Unsigned: uint64(b[0])}, nil

// 	case emulator.I8:
// 		if len(b) != 1 {
// 			return emulator.Value{}, errors.New("i8 must be 1 byte")
// 		}
// 		return emulator.Value{Type: t, Signed: int64(int8(b[0]))}, nil

// 	case emulator.U16:
// 		if len(b) != 2 {
// 			return emulator.Value{}, errors.New("u16 must be 2 bytes")
// 		}
// 		return emulator.Value{Type: t, Unsigned: uint64(binary.LittleEndian.Uint16(b))}, nil

// 	case emulator.I16:
// 		if len(b) != 2 {
// 			return emulator.Value{}, errors.New("i16 must be 2 bytes")
// 		}
// 		return emulator.Value{Type: t, Signed: int64(int16(binary.LittleEndian.Uint16(b)))}, nil

// 	case emulator.U32:
// 		if len(b) != 4 {
// 			return emulator.Value{}, errors.New("u32 must be 4 bytes")
// 		}
// 		return emulator.Value{Type: t, Unsigned: uint64(binary.LittleEndian.Uint32(b))}, nil

// 	case emulator.I32:
// 		if len(b) != 4 {
// 			return emulator.Value{}, errors.New("i32 must be 4 bytes")
// 		}
// 		return emulator.Value{Type: t, Signed: int64(int32(binary.LittleEndian.Uint32(b)))}, nil

// 	case emulator.U64:
// 		if len(b) != 8 {
// 			return emulator.Value{}, errors.New("u64 must be 8 bytes")
// 		}
// 		return emulator.Value{Type: t, Unsigned: binary.LittleEndian.Uint64(b)}, nil

// 	case emulator.I64:
// 		if len(b) != 8 {
// 			return emulator.Value{}, errors.New("i64 must be 8 bytes")
// 		}
// 		return emulator.Value{Type: t, Signed: int64(binary.LittleEndian.Uint64(b))}, nil

// 	default:
// 		return emulator.Value{}, errors.New("unknown type")
// 	}
// }
