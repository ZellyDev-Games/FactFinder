package retroarch

import (
	"FactFinder/emulator"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"net"
	"strconv"
	"sync"
	"time"
)

const wramOffset = 0x7e0000

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

func (c *Client) GetValues(readplan *emulator.ReadPlan) ([]emulator.Value, error) {
	{
		_ = c.conn.SetReadDeadline(time.Now())
		for {
			if _, err := c.conn.Read(c.respBuf); err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break
				}
				return nil, err
			}
		}
		_ = c.conn.SetReadDeadline(time.Time{})
	}

	vals := make([]emulator.Value, 0, len(readplan.Watches))

	for _, spec := range readplan.Watches {
		address := 0
		if spec.Bank == emulator.WRAM {
			address = int(spec.Address) + wramOffset
		}
		if spec.Bank == emulator.SRAM {
			if readplan.HiROM {
				address = 0x300000 + 0x6000 + (int(spec.Address) % 0xA000) + (int(spec.Address)/0xA000)*0x10000
			} else {
				address = 0x700000 + (int(spec.Address) % 0x8000) + (int(spec.Address)/0x8000)*0x10000
			}
		}

		size := spec.SizeOverride
		if size == 0 {
			size = spec.Size()
		}
		msg := c.buildReadCoreMemoryCmd(address, size)
		c.m.Lock()
		if _, err := c.conn.Write(msg); err != nil {
			c.m.Unlock()
			c.emulatorConnected = emulator.Reconnecting
			fmt.Printf("failed to write message to connection, signaling reconnect.: %s\n", err)
			return nil, err
		}

		_ = c.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 500))
		n, err := c.conn.Read(c.respBuf)
		if err != nil {
			c.m.Unlock()
			c.emulatorConnected = emulator.Reconnecting
			fmt.Printf("failed to read message from connection, signaling reconnect.: %s\n", err)
			return nil, err
		}
		c.m.Unlock()

		need := spec.Size()
		if cap(c.byteBuf) < need {
			c.byteBuf = make([]byte, need)
		}
		raw := c.byteBuf[:need]

		if err = decodeRetroArchReadCoreMemoryBytes(c.respBuf[:n], raw, need); err != nil {
			if errors.Is(err, emulator.ErrGameNotLoaded) {
				return nil, err
			}

			return nil, fmt.Errorf("decode failed for %s: %w", spec.Name, err)
		}

		val := emulator.Value{Type: spec.Type, Name: spec.Name}

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
			return nil, fmt.Errorf("unsupported size %d", need)
		}

		switch spec.Type {
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
			if spec.Mask != 0 {
				u = u & 0x3FFF
			}
			val.FlagCount = bits.OnesCount64(u)
		}

		vals = append(vals, val)
	}

	fmt.Println(vals)
	return vals, nil
}

func (c *Client) EmulatorConnected() emulator.ConnectionStatus {
	return c.emulatorConnected
}

func (c *Client) GameConnected() bool {
	return c.gameConnected
}
