package retroarch

import (
	"FactFinder/emulator"
	"FactFinder/logger"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

var log = logger.Module("retroarch").SetLevel(logger.ErrorLevel)

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
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic in ConnectEmulator: %v", r)
			c.emulatorConnected = emulator.Disconnected
		}
	}()

	conn, err := net.DialUDP("udp", nil, c.addr)
	if err != nil {
		log.Error("failed to connect UDP emulator: %v", err)
		fmt.Println(err)
		return emulator.Disconnected
	}
	c.conn = conn

	_, err = c.conn.Write([]byte("VERSION"))
	if err != nil {
		log.Debug("VERSION request failed: %v", err)
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(time.Second * 1))
	n, _, err := c.conn.ReadFromUDP(c.respBuf)
	if err != nil {
		log.Debug("VERSION handshake timeout: %v", err)
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	log.Info("retroarch handshake completed")

	if n > 0 {
		_ = c.conn.SetReadDeadline(time.Time{})
	}

	c.emulatorConnected = emulator.Connected
	log.Info("retroarch UDP connected: %s", c.addr.String())
	return emulator.Connected
}

func (c *Client) Close() error {
	log.Info("closing retroarch connection")
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

func (c *Client) CompileReadPlan(
	plan *emulator.ReadPlan,
) *emulator.CompiledReadPlan {
	return emulator.CompileReadPlan(
		plan,
		emulator.ResolveAddress,
		retroarchAddress,
	)
}

func retroarchAddress(
	plan *emulator.ReadPlan,
	spec emulator.ReadSpec,
	addr int,
) int {
	switch spec.Bank {
	case emulator.WRAM:
		// GB & GBC 0xC000-0xDFFF
		return addr + wramOffset

	case emulator.IWRAM:
		// GBA 0x19000 – 0x20FFF
		return addr + iwramOffset

	case emulator.EWRAM:
		// GBA 0x21000 – 0x60FFF
		return addr + ewramOffset

	case emulator.FCRAM:
		// 3DS 0x20000000-0x28000000
		return addr + fcramOffset

	case emulator.PSRAM:
		// DeSmuME 0x02000000-0x02400000
		// MelonDS 0x00000000-0x00400000
		return addr + psramOffset

	case emulator.RAM:
		if plan.Platform == "PSX" {

			// PSX 0x010000-0x200000
			return addr + psxRAMOffset
		}
	}

	// NES 0x0000-0x07FF
	// Genesis 0xFF0000-0xFFFFFF
	// 0x00000000 – 0x003FFFFF No expansion pack
	// 0x00000000 – 0x007FFFFF With expansion pack
	return addr
}

func (c *Client) GetValues(plan *emulator.CompiledReadPlan) ([]emulator.Value, error) {
	vals := make([]emulator.Value, 0)

	log.Debug("retroarch read cycle: regions=%d", len(plan.Regions))

	for _, region := range plan.Regions {
		msg := c.buildReadCoreMemoryCmd(
			region.Start,
			region.Size,
		)

		c.m.Lock()

		log.Debug("reading region start=0x%x size=%d", region.Start, region.Size)

		_, err := c.conn.Write(msg)
		if err != nil {
			c.m.Unlock()
			log.Error("UDP write failed: %v", err)
			return nil, err
		}

		_ = c.conn.SetReadDeadline(
			time.Now().Add(500 * time.Millisecond),
		)

		n, err := c.conn.Read(c.respBuf)

		c.m.Unlock()

		if err != nil {
			log.Error("UDP read failed: %v", err)
			return nil, err
		}

		err = decodeRetroArchReadCoreMemoryBytes(
			c.respBuf[:n],
			region.Buffer,
			region.Size,
		)
		if err != nil {
			log.Error("decode failed for region start=0x%x size=%d", region.Start, region.Size)
			return nil, err
		}

		for _, watch := range region.Watches {
			raw := region.Buffer[watch.Offset : watch.Offset+watch.Size]

			val := emulator.DecodeValue(watch.Spec, raw)

			if val == nil {
				return nil, fmt.Errorf("unsupported size %d", watch.Size)
			}

			vals = append(vals, *val)
		}
	}

	log.Debug("retroarch read cycle completed: values=%d", len(vals))
	return vals, nil
}
