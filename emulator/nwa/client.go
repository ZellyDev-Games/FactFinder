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
	"net"
	"strings"
	"sync"
	"time"
)

var log = logger.Module("emulator/nwa/client").SetLevel(logger.ErrorLevel)

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

	info, ok := summary.(hash)
	if !ok {
		log.Error("unexpected EMULATOR_INFO type %T", summary)
		c.emulatorConnected = emulator.Disconnected
		return emulator.Disconnected
	}

	log.Info(
		"connected to %s %s (NWA %s)",
		info["name"],
		info["version"],
		info["nwa_version"],
	)

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
		log.Error("MY_NAME_IS failed: %v", err)
		return
	}

	log.Info("MY_NAME_IS response: %#v", summary)
}

func (c *Client) EmuInfo() (EmulatorReply, error) {
	cmd := "EMULATOR_INFO"
	args := "0"
	summary, err := c.ExecuteCommand(cmd, &args)
	if err != nil {
		log.Error("EMULATOR_INFO failed: %v", err)
		return nil, err
	}

	log.Debug("EMULATOR_INFO response: %#v", summary)
	return summary, nil
}

func (c *Client) EmuGameInfo() {
	cmd := "GAME_INFO"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		log.Error("GAME_INFO failed: %v", err)
		return
	}

	log.Info("GAME_INFO response: %#v", summary)
}

func (c *Client) EmuStatus() {
	cmd := "EMULATION_STATUS"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		log.Error("EMULATION_STATUS failed: %v", err)
		return
	}

	log.Info("EMULATION_STATUS response: %#v", summary)
}

func (c *Client) CoreInfo() {
	cmd := "CORE_CURRENT_INFO"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		log.Error("CORE_CURRENT_INFO failed: %v", err)
		return
	}

	log.Info("CORE_CURRENT_INFO response: %#v", summary)
}

func (c *Client) CoreMemories() {
	summary, err := c.ExecuteCommand("CORE_MEMORIES", nil)
	if err != nil {
		log.Error("CORE_MEMORIES failed: %v", err)
		return
	}

	log.Info("CORE_MEMORIES response: %#v", summary)
}

func (c *Client) SoftResetConsole() {
	cmd := "EMULATION_RESET"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		log.Error("EMULATION_RESET failed: %v", err)
		return
	}

	log.Info("EMULATION_RESET response: %#v", summary)
}

func (c *Client) HardResetConsole() {
	// cmd := "EMULATION_STOP"
	cmd := "EMULATION_RELOAD"
	summary, err := c.ExecuteCommand(cmd, nil)
	if err != nil {
		log.Error("EMULATION_RELOAD failed: %v", err)
		return
	}

	log.Info("EMULATION_RELOAD response: %#v", summary)
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
		nil,
	)
}

func domainForBank(bank emulator.Bank) string {
	switch bank {
	case emulator.WRAM:
		return "WRAM"

	case emulator.RAM:
		return "RAM"

	case emulator.IWRAM:
		return "IWRAM"

	case emulator.EWRAM:
		return "EWRAM"

	case emulator.FCRAM:
		return "FCRAM"

	case emulator.PSRAM:
		return "PSRAM"

	case emulator.RDRAM:
		return "RDRAM"

	default:
		return "RAM"
	}
}

func (c *Client) GetValues(plan *emulator.CompiledReadPlan) ([]emulator.Value, error) {
	log.Debug("reading %d merged regions", len(plan.Regions))

	vals := make([]emulator.Value, 0)

	cmd := "CORE_READ"

	for _, region := range plan.Regions {
		domain := domainForBank(region.Bank)

		args := fmt.Sprintf(
			"%s;$%X;%d",
			domain,
			region.Start,
			region.Size,
		)

		log.Debug(
			"CORE_READ domain=%s bank=%s start=$%X size=%d args=%q",
			domain,
			region.Bank,
			region.Start,
			region.Size,
			args,
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

		var data []byte

		switch v := summary.(type) {
		case []byte:
			data = v
		case Error:
			log.Error(
				"CORE_READ rejected: bank=%s start=$%X size=%d kind=%v reason=%s",
				region.Bank,
				region.Start,
				region.Size,
				v.Kind,
				v.Reason,
			)

			return nil, fmt.Errorf(
				"CORE_READ rejected: %s",
				v.Reason,
			)
		case hash:
			log.Error(
				"CORE_READ returned hash instead of binary: %#v",
				v,
			)

			return nil, fmt.Errorf(
				"CORE_READ returned hash response",
			)
		default:
			log.Error(
				"unexpected CORE_READ response type %T value=%#v",
				summary,
				summary,
			)

			return nil, fmt.Errorf(
				"unexpected CORE_READ response type %T",
				summary,
			)
		}

		log.Debug(
			"CORE_READ returned %d bytes for %s @ $%X",
			len(data),
			domain,
			region.Start,
		)

		preview := len(data)
		if preview > 16 {
			preview = 16
		}

		log.Debug(
			"CORE_READ first %d bytes: % X",
			preview,
			data[:preview],
		)

		if len(data) < region.Size {
			return nil, fmt.Errorf(
				"short read: expected %d bytes, got %d",
				region.Size,
				len(data),
			)
		}

		for _, watch := range region.Watches {
			raw := data[watch.Offset : watch.Offset+watch.Size]

			val := emulator.DecodeValue(watch.Spec, raw)

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
