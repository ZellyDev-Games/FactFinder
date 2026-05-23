package nwa

import (
	"FactFinder/emulator"
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxGap = 16
const maxReadSize = 4096

type Error struct {
	Kind   errorKind
	Reason string
}

type Client struct {
	m                 sync.Mutex
	conn              *net.TCPConn
	emulatorConnected emulator.ConnectionStatus
	addr              *net.TCPAddr
	gameConnected     bool

	respBuf []byte
	byteBuf []byte
}

func NewClient(ip, port string) *Client {
	// address := fmt.Sprintf("%s:%s", ip, port)
	tcpAddr, _ := net.ResolveTCPAddr("tcp", ip+":"+port)
	// if err != nil {
	// return nil, fmt.Errorf("can't resolve address: %w", err)
	// }

	return &Client{
		addr:    tcpAddr,
		respBuf: make([]byte, 4096),
		byteBuf: make([]byte, 0, 16),
	}
}

func (c *Client) ConnectEmulator() emulator.ConnectionStatus {
	conn, err := net.DialTCP("tcp", nil, c.addr)
	if err != nil {
		fmt.Println(err)
		return emulator.Disconnected
	}
	c.conn = conn

	for {
		_, err = c.conn.Write([]byte("VERSION"))
		if err != nil {
			fmt.Println(err)
			continue
		}

		_ = c.conn.SetReadDeadline(time.Now().Add(time.Second * 1))
		n, err := c.conn.Read(c.respBuf)
		if err != nil {
			fmt.Println(err)
			continue
		}

		if n > 0 {
			_ = c.conn.SetReadDeadline(time.Time{})
			break
		}
	}

	c.emulatorConnected = emulator.Connected
	return emulator.Connected
}

func (c *Client) ExecuteCommand(cmd string, argString *string) (EmulatorReply, error) {
	var command string
	_ = c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if argString == nil {
		command = fmt.Sprintf("%s\n", cmd)
	} else {
		command = fmt.Sprintf("%s %s\n", cmd, *argString)
	}

	// c.m.Lock()
	_, err := io.WriteString(c.conn, command)
	if err != nil {
		// c.m.Unlock()
		return nil, err
	}

	return c.getReply()
}

// func (c *Client) ExecuteRawCommand(cmd string, argString *string) {
// 	var command string
// 	_ = c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
// 	if argString == nil {
// 		command = fmt.Sprintf("%s\n", cmd)
// 	} else {
// 		command = fmt.Sprintf("%s %s\n", cmd, *argString)
// 	}

// 	// ignoring error as per TODO in Rust code
// 	_, _ = io.WriteString(c.conn, command)
// }

// func (c *SyncClient) IsConnected() bool {
// 	// net.Conn in Go does not have a Peek method.
// 	// We can try to set a read deadline and read with a zero-length buffer to check connection.
// 	// But zero-length read returns immediately, so we try to read 1 byte with deadline.
// 	buf := make([]byte, 1)
// 	_ = c.Connection.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
// 	n, err := c.Connection.Read(buf)
// 	if err != nil {
// 		// If timeout or no data, consider connected
// 		netErr, ok := err.(net.Error)
// 		if ok && netErr.Timeout() {
// 			return true
// 		}
// 		return false
// 	}
// 	if n > 0 {
// 		// Data was read, connection is alive
// 		return true
// 	}
// 	return false
// }

func (c *Client) Close() error {
	c.emulatorConnected = emulator.Disconnected
	c.gameConnected = false
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// func (c *Client) Reconnected() (bool, error) {
// 	conn, err := net.DialTimeout("tcp", c.addr.String(), time.Second)
// 	if err != nil {
// 		return false, err
// 	}
// 	c.conn = conn
// 	return true, nil
// }

// private
type errorKind int

const (
	InvalidError errorKind = iota
	InvalidCommand
	InvalidArgument
	NotAllowed
	ProtocolError
)

type hash map[string]string

type EmulatorReply interface{}

func (c *Client) getReply() (EmulatorReply, error) {
	readStream := bufio.NewReader(c.conn)
	firstByte, err := readStream.ReadByte()
	if err != nil {
		if err == io.EOF {
			return nil, errors.New("connection aborted")
		}
		return nil, err
	}

	// Ascii
	// stops reading when the only result is a new line
	if firstByte == '\n' {
		mapResult := make(map[string]string)
		for {
			line, err := readStream.ReadBytes('\n')
			if err != nil {
				return nil, err
			}
			if len(line) == 0 {
				break
			}
			if line[0] == '\n' && len(mapResult) == 0 {
				return nil, nil
			}
			if line[0] == '\n' {
				break
			}
			colonIndex := bytes.IndexByte(line, ':')
			if colonIndex == -1 {
				return nil, errors.New("malformed line, missing ':'")
			}
			key := strings.TrimSpace(string(line[:colonIndex]))
			value := strings.TrimSpace(string(line[colonIndex+1 : len(line)-1])) // remove trailing \n
			mapResult[key] = value
		}
		if _, ok := mapResult["error"]; ok {
			reason, hasReason := mapResult["reason"]
			errorStr, hasError := mapResult["error"]
			if hasReason && hasError {
				var mkind errorKind
				switch errorStr {
				case "protocol_error":
					mkind = ProtocolError
				case "invalid_command":
					mkind = InvalidCommand
				case "invalid_argument":
					mkind = InvalidArgument
				case "not_allowed":
					mkind = NotAllowed
				default:
					mkind = InvalidError
				}
				return Error{
					Kind:   mkind,
					Reason: reason,
				}, nil
			} else {
				return Error{
					Kind:   InvalidError,
					Reason: "Invalid reason",
				}, nil
			}
		}
		return hash(mapResult), nil
	}

	// Binary
	if firstByte == 0 {
		header := make([]byte, 4)
		n, err := io.ReadFull(readStream, header)
		if err != nil || n != 4 {
			return nil, errors.New("failed to read header")
		}
		size := binary.BigEndian.Uint32(header)
		data := make([]byte, size)
		_, err = io.ReadFull(readStream, data)
		if err != nil {
			return nil, err
		}
		return data, nil
	}

	return nil, errors.New("invalid reply")
}

// This would be used if I actually sent data
// func (c *SyncClient) sendData(data []byte) {
// 	buf := make([]byte, 5)
// 	size := len(data)
// 	buf[0] = 0
// 	buf[1] = byte((size >> 24) & 0xFF)
// 	buf[2] = byte((size >> 16) & 0xFF)
// 	buf[3] = byte((size >> 8) & 0xFF)
// 	buf[4] = byte(size & 0xFF)
// 	// TODO: handle the error
// 	c.Connection.Write(buf)
// 	// TODO: handle the error
// 	c.Connection.Write(data)
// }

func (c *Client) ClientID() {
	cmd := "MY_NAME_IS"
	args := "OpenSplit"
	summary, err := c.ExecuteCommand(cmd, &args)
	if err != nil {
		// panic(err)
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) EmuInfo() {
	cmd := "EMULATOR_INFO"
	args := "0"
	summary, err := c.ExecuteCommand(cmd, &args)
	if err != nil {
		println(err)
		// panic(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) EmuGameInfo() {
	cmd := "GAME_INFO"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		// panic(err)
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) EmuStatus() {
	cmd := "EMULATION_STATUS"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		// panic(err)
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) CoreInfo() {
	cmd := "CORE_CURRENT_INFO"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		// panic(err)
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) CoreMemories() {
	cmd := "CORE_MEMORIES"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		// panic(err)
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) SoftResetConsole() {
	cmd := "EMULATION_RESET"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		// panic(err)
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) HardResetConsole() {
	// cmd := "EMULATION_STOP"
	cmd := "EMULATION_RELOAD"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		// panic(err)
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) EmulatorConnected() emulator.ConnectionStatus {
	return c.emulatorConnected
}

func (c *Client) GameConnected() bool {
	return c.gameConnected
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

func (c *Client) GetValues(plan *emulator.CompiledReadPlan) ([]emulator.Value, error) {
	vals := make([]emulator.Value, 0)

	cmd := "CORE_READ"
	// var domain string
	// var requestString string

	// for _, watcher := range c.nwaMemory {
	// requestString += ";" + watcher.address + ";" + watcher.size
	// }

	// args := domain + requestString
	// 	I8        ValueType = "I8"
	// 	I16       ValueType = "I16"
	// 	I32       ValueType = "I32"
	// 	I64       ValueType = "I64"
	// 	U8        ValueType = "U8"
	// 	U16       ValueType = "U16"
	// 	U32       ValueType = "U32"
	// 	U64       ValueType = "U64"
	// 	Bool      ValueType = "Bool"
	// 	FlagCount           = "FlagCount"
	for _, region := range plan.Regions {
		args := string(region.Bank) + ";" + strconv.Itoa(region.Start) + ";" + strconv.Itoa(region.Size)

		summary, err := c.ExecuteCommand(cmd, &args)
		if err != nil {
			return nil, err
		}

		data, ok := summary.([]byte)
		if !ok {
			return nil, fmt.Errorf("unexpected CORE_READ response type %T", summary)
		}

		if len(data) < region.Size {
			return nil, fmt.Errorf(
				"short read: expected %d bytes, got %d",
				region.Size,
				len(data),
			)
		}

		// Store merged region buffer
		// copy(region.Buffer, data[:region.Size])

		for _, watch := range region.Watches {
			// raw := region.Buffer[watch.Offset : watch.Offset+watch.Size]
			raw := data[watch.Offset : watch.Offset+watch.Size]

			val := decodeValue(watch.Spec, raw)
			if val == nil {
				return nil, fmt.Errorf(
					"unsupported value decode size %d",
					watch.Size,
				)
			}

			vals = append(vals, *val)
		}
	}
	// if err != nil {
	// 	return Summary{}, err
	// }
	// fmt.Printf("%#v\n", summary)

	// switch v := summary.(type) {
	// case []byte:
	// 	// update memoryWatcher with data
	// 	runningTotal := 0
	// 	for _, watcher := range b.nwaMemory {
	// 		size, _ := strconv.Atoi(watcher.size)
	// 		switch size {
	// 		case 1:
	// 			*watcher.currentValue = int(v[runningTotal])
	// 			runningTotal += size
	// 		case 2:
	// 			*watcher.currentValue = int(binary.LittleEndian.Uint16(v[runningTotal : runningTotal+size]))
	// 			runningTotal += size
	// 		case 3:
	// 			fallthrough
	// 		case 4:
	// 			*watcher.currentValue = int(binary.LittleEndian.Uint32(v[runningTotal : runningTotal+size]))
	// 			runningTotal += size
	// 		case 5:
	// 			fallthrough
	// 		case 6:
	// 			fallthrough
	// 		case 7:
	// 			fallthrough
	// 		case 8:
	// 			*watcher.currentValue = int(binary.LittleEndian.Uint64(v[runningTotal : runningTotal+size]))
	// 			runningTotal += size
	// 		}
	// 	}

	// case Error:
	// 	fmt.Printf("%#v\n", v)
	// default:
	// 	fmt.Printf("%#v\n", v)
	// }
	return vals, nil
}

// GB/GBC/GBA/SNES/NES/DS/3DS/PSX = little endian
// Genesis/N64 = big endian
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
