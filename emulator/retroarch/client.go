package retroarch

import (
	"FactFinder/emulator"
	"encoding/binary"
	"fmt"
	"math/bits"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"
)

const wramOffset = 0x7e0000
const iwramOffset = 0x19000
const ewramOffset = 0x21000
const fcramOffset = 0x20000000
const psramOffset = 0x02000000
const psxRAMOffset = 0x010000

const maxGap = 16
const maxReadSize = 4096

type Client struct {
	m                 sync.Mutex
	conn              *net.UDPConn
	emulatorConnected emulator.ConnectionStatus
	addr              *net.UDPAddr
	gameConnected     bool

	respBuf []byte
	byteBuf []byte
	cmdBuf  []byte
}

func NewClient(host, port string) *Client {
	addr, _ := net.ResolveUDPAddr("udp", host+":"+port)
	return &Client{
		addr:    addr,
		respBuf: make([]byte, 4096),
		byteBuf: make([]byte, 0, 16),
		cmdBuf:  make([]byte, 0, 64),
	}
}

func (c *Client) ConnectEmulator() emulator.ConnectionStatus {
	conn, err := net.DialUDP("udp", nil, c.addr)
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
		n, _, err := c.conn.ReadFromUDP(c.respBuf)
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

func (c *Client) Close() error {
	c.emulatorConnected = emulator.Disconnected
	c.gameConnected = false
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) buildReadCoreMemoryCmd(address int, size int) []byte {
	c.cmdBuf = c.cmdBuf[:0]
	c.cmdBuf = append(c.cmdBuf, "READ_CORE_MEMORY "...)
	c.cmdBuf = appendHexUpper(c.cmdBuf, uint64(address))
	c.cmdBuf = append(c.cmdBuf, ' ')
	c.cmdBuf = strconv.AppendInt(c.cmdBuf, int64(size), 10)
	return c.cmdBuf
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

	vals := make([]emulator.Value, 0)

	for _, region := range plan.Regions {
		msg := c.buildReadCoreMemoryCmd(
			region.Start,
			region.Size,
		)

		c.m.Lock()

		_, err := c.conn.Write(msg)
		if err != nil {
			c.m.Unlock()
			return nil, err
		}

		_ = c.conn.SetReadDeadline(
			time.Now().Add(500 * time.Millisecond),
		)

		n, err := c.conn.Read(c.respBuf)

		c.m.Unlock()

		if err != nil {
			return nil, err
		}

		err = decodeRetroArchReadCoreMemoryBytes(
			c.respBuf[:n],
			region.Buffer,
			region.Size,
		)
		if err != nil {
			return nil, err
		}

		for _, watch := range region.Watches {
			raw := region.Buffer[watch.Offset : watch.Offset+watch.Size]

			val := decodeValue(watch.Spec, raw)

			if val == nil {
				return nil, fmt.Errorf("unsupported size %d", watch.Size)
			}

			vals = append(vals, *val)
		}
	}

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
