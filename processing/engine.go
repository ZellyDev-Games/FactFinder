package processing

import (
	"FactFinder/emulator"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

type Command byte

const (
	QUIT Command = iota
	NEW
	LOAD
	EDIT
	CANCEL
	SUBMIT
	CLOSE
	RESET
	SAVE
	SPLIT
	UNDO
	SKIP
	PAUSE
	TOGGLEGLOBAL
	FOCUS
	HELLO
)

type Engine struct {
	L                    *lua.LState
	m                    sync.Mutex
	values               map[string]emulator.Value
	conn                 net.PacketConn
	osAddr               *net.UDPAddr
	openSplitConnected   bool
	opensplitConnectedCh chan bool
	tickFunc             *lua.LFunction
}

func NewEngine() (*Engine, chan bool) {
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		panic(err)
	}

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:6767")
	if err != nil {
		panic(err)
	}

	e := &Engine{
		m:                    sync.Mutex{},
		values:               make(map[string]emulator.Value),
		conn:                 conn,
		osAddr:               addr,
		opensplitConnectedCh: make(chan bool),
	}

	go func() {
		ticker := time.NewTicker(1000 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			e.openSplitConnected = e.Hello()
			e.updateConnectionStatus(e.openSplitConnected)
		}
	}()

	return e, e.opensplitConnectedCh
}

func (e *Engine) Close() {
	if e.L != nil {
		e.L.Close()
	}
	e.updateConnectionStatus(false)
	_ = e.conn.Close()
	e.conn = nil
}

func (e *Engine) OpenSplitConnected() bool {
	return e.openSplitConnected
}

func (e *Engine) LoadFile(path string, plan *emulator.ReadPlan) error {
	L := lua.NewState()
	e.L = L

	for _, spec := range plan.Watches {
		if spec.Type == emulator.Bool {
			e.L.SetGlobal(spec.Name, lua.LBool(false))
			e.L.SetGlobal(spec.Name+"_last", lua.LBool(false))
		} else {
			e.L.SetGlobal(spec.Name, lua.LNumber(0))
			e.L.SetGlobal(spec.Name+"_last", lua.LNumber(0))
		}
	}

	e.L.SetGlobal("split", e.L.NewFunction(func(L *lua.LState) int {
		packet := buildRCPacket(SPLIT, false)

		e.m.Lock()
		defer e.m.Unlock()

		_, err := e.conn.WriteTo(packet, e.osAddr)
		if err != nil {
			fmt.Println(err)
			e.updateConnectionStatus(false)
			return 1
		}
		e.updateConnectionStatus(true)
		return 0
	}))

	e.L.SetGlobal("reset", e.L.NewFunction(func(L *lua.LState) int {
		packet := buildRCPacket(RESET, false)

		e.m.Lock()
		defer e.m.Unlock()

		_, err := e.conn.WriteTo(packet, e.osAddr)
		if err != nil {
			e.updateConnectionStatus(false)
			fmt.Println(err)
			return 1
		}
		e.updateConnectionStatus(true)
		return 0
	}))

	e.L.SetGlobal("pause", e.L.NewFunction(func(L *lua.LState) int {
		packet := buildRCPacket(PAUSE, false)

		e.m.Lock()
		defer e.m.Unlock()

		_, err := e.conn.WriteTo(packet, e.osAddr)
		if err != nil {
			e.updateConnectionStatus(false)
			fmt.Println(err)
			return 1
		}
		e.updateConnectionStatus(true)
		return 0
	}))

	if err := e.L.DoFile(path); err != nil {
		return err
	}

	fn := e.L.GetGlobal("onTick")
	if fn.Type() != lua.LTFunction {
		fmt.Println("onTick not present in factfinder.lua")
	} else {
		e.tickFunc = fn.(*lua.LFunction)
	}

	return nil
}

func (e *Engine) GetState() [][]string {
	out := make([][]string, 0)

	tbl, ok := e.L.GetGlobal("state").(*lua.LTable)
	if !ok {
		return out
	}

	tbl.ForEach(func(k, v lua.LValue) {
		out = append(out, []string{k.String(), v.String()})
	})

	sort.Slice(out, func(i, j int) bool {
		return out[i][0] < out[j][0] // sort by key
	})

	return out
}

func (e *Engine) ProcessValues(values []emulator.Value) error {
	for _, newValue := range values {
		name := newValue.Name
		t := newValue.Type

		// First time we've seen this value, do no processing on it yet.
		if _, ok := e.values[name]; !ok {
			e.values[name] = newValue
			continue
		}

		switch t {
		case emulator.FlagCount:
			e.L.SetGlobal(name+"_last", lua.LNumber(e.values[name].FlagCount))
			e.L.SetGlobal(name, lua.LNumber(newValue.FlagCount))
		case emulator.Bool:
			e.L.SetGlobal(name+"_last", lua.LBool(e.values[name].Bool))
			e.L.SetGlobal(name, lua.LBool(newValue.Bool))
		case emulator.U8, emulator.U16, emulator.U32, emulator.U64:
			e.L.SetGlobal(name+"_last", lua.LNumber(e.values[name].Unsigned))
			e.L.SetGlobal(name, lua.LNumber(newValue.Unsigned))
		default:
			e.L.SetGlobal(name+"_last", lua.LNumber(e.values[name].Signed))
			e.L.SetGlobal(name, lua.LNumber(newValue.Signed))
		}

		e.values[name] = newValue
	}

	err := e.L.CallByParam(lua.P{
		Fn:      e.tickFunc,
		NRet:    0,
		Protect: true,
	})
	if err != nil {
		return err
	}
	return nil
}

func (e *Engine) Hello() bool {
	packet := buildRCPacket(HELLO, true)

	e.m.Lock()
	_, err := e.conn.WriteTo(packet, e.osAddr)
	if err != nil {
		e.m.Unlock()
		fmt.Println(err)
		return false
	}

	buf := make([]byte, 7)
	_ = e.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, _, err = e.conn.ReadFrom(buf)
	if err != nil || buf[6] != 0 {
		e.m.Unlock()
		fmt.Println(err)
		return false
	}
	e.m.Unlock()
	return true
}

func buildRCPacket(command Command, requestAck bool) []byte {
	var payload = make([]byte, 7)
	payload[0] = 'O' //magic
	payload[1] = 'S'
	payload[2] = 'R'
	payload[3] = 'C'
	payload[4] = 1 // version
	if requestAck {
		payload[5] = 1
	} else {
		payload[5] = 0
	}
	payload[6] = byte(command)

	return payload
}

func (e *Engine) updateConnectionStatus(status bool) {
	e.openSplitConnected = status
	select {
	case e.opensplitConnectedCh <- e.openSplitConnected:
	default:
	}
}
