package nwa

import (
	"FactFinder/emulator"
	"FactFinder/logger"
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

var log = logger.Module("emulator/nwa/client").SetLevel(logger.ErrorLevel)

const wramOffset = 0x7e0000
const iwramOffset = 0x19000
const ewramOffset = 0x21000
const fcramOffset = 0x20000000
const psramOffset = 0x02000000
const psxRAMOffset = 0x010000

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
	tcpAddr, _ := net.ResolveTCPAddr("tcp", ip+":"+port)

	log.Info("creating nwa client for %s", tcpAddr.String())

	return &Client{
		addr:    tcpAddr,
		respBuf: make([]byte, 4096),
		byteBuf: make([]byte, 0, 16),
	}
}

func (c *Client) ConnectEmulator() emulator.ConnectionStatus {
	log.Info("attempting emulator connection to %s", c.addr.String())

	defer func() {
		if r := recover(); r != nil {
			log.Error("panic in ConnectEmulator: %v", r)
			c.emulatorConnected = emulator.Disconnected
		}
	}()

	conn, err := net.DialTCP("tcp", nil, c.addr)
	if err != nil {
		log.Error("tcp dial failed: %v", err)
		return emulator.Disconnected
	}

	log.Info("tcp connection established")

	c.conn = conn

	summary, err := c.EmuInfo()
	if err != nil {
		log.Error("EmuInfo failed: %v", err)
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	if summary != nil {
		log.Error("unexpected emulator info response: %#v", summary)

		_ = c.conn.SetReadDeadline(time.Time{})
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	log.Info("connected to emulator successfully")

	c.CoreMemories()

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

	log.Debug("sending command: %q", strings.TrimSpace(command))

	_, err := io.WriteString(c.conn, command)
	if err != nil {
		log.Error("command write failed: %v", err)
		return nil, err
	}

	start := time.Now()
	reply, err := c.getReply()
	switch v := reply.(type) {
	case nil:
		log.Debug("reply=nil")
	case hash:
		log.Debug("reply=hash keys=%d", len(v))
	case []byte:
		log.Debug("reply=binary bytes=%d", len(v))
	case Error:
		log.Warn(
			"reply=protocol error kind=%v reason=%s",
			v.Kind,
			v.Reason,
		)
	default:
		log.Warn("reply=unknown type=%T", v)
	}
	duration := time.Since(start)

	if err != nil {
		log.Error(
			"command %s failed after %s: %v",
			cmd,
			duration,
			err,
		)
		return nil, err
	}

	log.Debug(
		"command %s completed in %s reply=%T",
		cmd,
		duration,
		reply,
	)

	return reply, nil
}

func (c *Client) Close() error {
	log.Info("closing nwa client")

	c.emulatorConnected = emulator.Disconnected
	c.gameConnected = false

	if c.conn == nil {
		log.Debug("close skipped: no active connection")
		return nil
	}

	err := c.conn.Close()
	if err != nil {
		log.Warn("tcp close failed: %v", err)
		return err
	}

	log.Info("tcp connection closed")

	return nil
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
			log.Warn("emulator disconnected")
			return nil, errors.New("connection aborted")
		}

		log.Error("failed reading reply byte: %v", err)
		return nil, err
	}

	log.Debug("reply first byte: %d", firstByte)

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
				log.Error("malformed ascii reply line: %q", string(line))
				return nil, errors.New("malformed line, missing ':'")
			}
			key := strings.TrimSpace(string(line[:colonIndex]))
			value := strings.TrimSpace(string(line[colonIndex+1 : len(line)-1])) // remove trailing \n
			mapResult[key] = value
		}
		log.Debug("ascii reply parsed: %#v", mapResult)

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
		log.Debug(
			"binary reply incoming size=%d bytes",
			size,
		)
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
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) EmuInfo() (EmulatorReply, error) {
	cmd := "EMULATOR_INFO"
	args := "0"
	summary, err := c.ExecuteCommand(cmd, &args)
	if err != nil {
		println(err)
		return nil, err
	}
	fmt.Printf("%#v\n", summary)
	return summary, nil
}

func (c *Client) EmuGameInfo() {
	cmd := "GAME_INFO"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) EmuStatus() {
	cmd := "EMULATION_STATUS"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) CoreInfo() {
	cmd := "CORE_CURRENT_INFO"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) CoreMemories() {
	cmd := "CORE_MEMORIES"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		println(err)
	}
	fmt.Printf("%#v\n", summary)
}

func (c *Client) SoftResetConsole() {
	cmd := "EMULATION_RESET"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
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
		addr := resolveAddress(plan, spec)

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
				log.Debug(
					"merged region bank=%s start=%X size=%d watches=%d",
					cur.Bank,
					cur.Start,
					cur.Size,
					len(cur.Watches),
				)
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
	log.Info(
		"compiled read plan: %d watches -> %d merged regions",
		len(plan.Watches),
		len(out.Regions),
	)
	return out
}

func resolveAddress(plan *emulator.ReadPlan, spec emulator.ReadSpec) int {
	switch spec.Bank {
	case emulator.WRAM:
		if plan.Platform == "SNES" {
			return int(spec.Address) + wramOffset
		}
		// GB & GBC 0xC000-0xDFFF
		return int(spec.Address)

	case emulator.SRAM:
		if plan.HiROM {
			return 0x300000 +
				0x6000 +
				(int(spec.Address) % 0xA000) +
				(int(spec.Address)/0xA000)*0x10000
		}

		return 0x700000 +
			(int(spec.Address) % 0x8000) +
			(int(spec.Address)/0x8000)*0x10000

	case emulator.RAM:
		// RAM   Bank = "ram"   // PSX/NES/Genesis Memory
		// NES 0x0000-0x07FF
		// Genesis 0xFF0000-0xFFFFFF
		if plan.Platform != "PSX" {
			return int(spec.Address)
		}
		// PSX 0x010000-0x200000
		return int(spec.Address) + psxRAMOffset

	case emulator.IWRAM:
		// IWRAM Bank = "iwram" // GBA Internal Memory
		// GBA 0x19000 – 0x20FFF
		return int(spec.Address) + iwramOffset

	case emulator.EWRAM:
		// EWRAM Bank = "ewram" // GBA External Memory
		// GBA 0x21000 – 0x60FFF
		return int(spec.Address) + ewramOffset

	case emulator.FCRAM:
		// FCRAM Bank = "fcram" // 3DS Memory
		// 3DS 0x20000000-0x28000000
		return int(spec.Address) + fcramOffset

	case emulator.PSRAM:
		// PSRAM Bank = "psram" // DS Memory
		// DeSmuME 0x02000000-0x02400000
		// MelonDS 0x00000000-0x00400000
		return int(spec.Address) + psramOffset

	case emulator.RDRAM:
		// RDRAM Bank = "rdram" // N64 Memory
		// 0x00000000 – 0x003FFFFF No expansion pack
		// 0x00000000 – 0x007FFFFF With expansion pack
		return int(spec.Address)
	}

	return 0
}

func (c *Client) GetValues(plan *emulator.CompiledReadPlan) ([]emulator.Value, error) {
	log.Debug("reading %d merged regions", len(plan.Regions))

	vals := make([]emulator.Value, 0)

	cmd := "CORE_READ"

	for _, region := range plan.Regions {
		args := fmt.Sprintf(
			"RAM;$%X;%d",
			region.Start,
			region.Size,
		)

		log.Debug(
			"CORE_READ bank=%s start=%X end=%X size=%d",
			region.Bank,
			region.Start,
			region.Start+region.Size,
			region.Size,
		)

		summary, err := c.ExecuteCommand(cmd, &args)
		if err != nil {
			log.Error("CORE_READ failed: %v", err)
			return nil, err
		}

		data, ok := summary.([]byte)
		if !ok {
			log.Error("unexpected CORE_READ response type %T", summary)
			return nil, fmt.Errorf("unexpected CORE_READ response type %T", summary)
		}

		log.Debug("CORE_READ returned %d bytes", len(data))

		if len(data) < region.Size {
			return nil, fmt.Errorf(
				"short read: expected %d bytes, got %d",
				region.Size,
				len(data),
			)
		}

		for _, watch := range region.Watches {
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
	log.Debug("decoded %d values", len(vals))

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
