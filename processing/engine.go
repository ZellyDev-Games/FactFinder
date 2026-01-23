package processing

import (
	"FactFinder/emulator"
	"fmt"
	"net"
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

type Signals uint32

const (
	delta Signals = 1 << iota
	rising
	falling
	edge
)

func (s *Signals) Has(signal Signals) bool {
	return *s&signal != 0
}

func (s *Signals) Set(signal Signals) {
	*s |= signal
}

type Engine struct {
	L                    *lua.LState
	m                    sync.Mutex
	values               map[string]emulator.Value
	signals              map[string]Signals
	deltaSigned          *lua.LFunction
	deltaUnsigned        *lua.LFunction
	risingSigned         *lua.LFunction
	fallingSigned        *lua.LFunction
	risingUnsigned       *lua.LFunction
	fallingUnsigned      *lua.LFunction
	edge                 *lua.LFunction
	conn                 net.PacketConn
	osAddr               *net.UDPAddr
	openSplitConnected   bool
	opensplitConnectedCh chan bool
}

func NewEngine() (*Engine, chan bool) {
	L := lua.NewState()
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		panic(err)
	}

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:6767")
	if err != nil {
		panic(err)
	}

	e := &Engine{
		L:                    L,
		values:               make(map[string]emulator.Value),
		signals:              make(map[string]Signals),
		conn:                 conn,
		osAddr:               addr,
		opensplitConnectedCh: make(chan bool),
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
	e.conn.Close()
	e.conn = nil
}

func (e *Engine) OpenSplitConnected() bool {
	return e.openSplitConnected
}

func (e *Engine) LoadFile(path string) error {
	if err := e.L.DoFile(path); err != nil {
		return err
	}
	e.cacheCallbacks()
	return nil
}

func (e *Engine) ProcessValues(readplan *emulator.ReadPlan, values []emulator.Value) error {
	for _, newValue := range values {
		if _, ok := e.values[newValue.Name]; !ok {
			// first time we've seen this value, get the value and the requested signals
			e.values[newValue.Name] = newValue
			signals := Signals(0)
			for _, w := range readplan.Watches {
				if w.Name == newValue.Name {
					for _, s := range w.Signals {
						if s == emulator.Rising {
							signals.Set(rising)
							continue
						}

						if s == emulator.Falling {
							signals.Set(falling)
							continue
						}

						if s == emulator.Delta {
							signals.Set(delta)
							continue
						}

						if s == emulator.Edge {
							signals.Set(edge)
							continue
						}
					}

					e.signals[newValue.Name] = signals
					break
				}
			}
			nvString := ""
			switch newValue.Type {
			case emulator.U8, emulator.U16, emulator.U32, emulator.U64:
				nvString = fmt.Sprintf("%d", newValue.Unsigned)
			case emulator.I8, emulator.I16, emulator.I32, emulator.I64:
				nvString = fmt.Sprintf("%d", newValue.Signed)
			case emulator.Bool:
				nvString = fmt.Sprintf("%t", newValue.Bool)
			}
			fmt.Printf("New Value: %s - %v\n", newValue.Name, nvString)
			continue
		}

		name := newValue.Name
		old := e.values[name]
		signals := e.signals[name]

		switch old.Type {
		case emulator.I8, emulator.I16, emulator.I32, emulator.I64:
			if old.Signed == newValue.Signed {
				// no change early exit
				continue
			}

			if signals.Has(delta) {
				fmt.Printf("delta: %s - %d -> %d\n", newValue.Name, old.Signed, newValue.Signed)
				e.DeltaSigned(name, old.Signed, newValue.Signed)
			}

			if signals.Has(rising) && newValue.Signed > old.Signed {
				fmt.Printf("rising: %s - %d -> %d\n", newValue.Name, old.Signed, newValue.Signed)
				e.RisingSigned(name, old.Signed, newValue.Signed)
			} else if signals.Has(falling) && newValue.Signed < old.Signed {
				fmt.Printf("falling: %s - %d -> %d\n", newValue.Name, old.Signed, newValue.Signed)
				e.FallingSigned(name, old.Signed, newValue.Signed)
			}

		case emulator.U8, emulator.U16, emulator.U32, emulator.U64:
			if old.Unsigned == newValue.Unsigned {
				// no change early exit
				continue
			}

			if signals.Has(delta) {
				fmt.Printf("delta: %s - %d -> %d\n", newValue.Name, old.Unsigned, newValue.Unsigned)
				e.DeltaUnsigned(name, old.Unsigned, newValue.Unsigned)
			}

			if signals.Has(rising) && newValue.Unsigned > old.Unsigned {
				fmt.Printf("rising: %s - %d -> %d\n", newValue.Name, old.Unsigned, newValue.Unsigned)
				e.RisingUnsigned(name, old.Unsigned, newValue.Unsigned)
			} else if signals.Has(falling) && newValue.Unsigned < old.Unsigned {
				fmt.Printf("falling: %s - %d -> %d\n", newValue.Name, old.Unsigned, newValue.Unsigned)
				e.FallingUnsigned(name, old.Unsigned, newValue.Unsigned)
			}

		case emulator.Bool:
			if signals.Has(edge) && newValue.Bool != old.Bool {
				fmt.Printf("edge: %s - %t -> %t\n", newValue.Name, old.Bool, newValue.Bool)
				e.Edge(name, newValue.Bool)
			}
		}
		e.values[name] = newValue
	}

	return nil
}

func (e *Engine) DeltaSigned(id string, old, new int64) {
	if e.deltaSigned == nil {
		return
	}

	_ = e.L.CallByParam(lua.P{
		Fn:      e.deltaSigned,
		NRet:    0,
		Protect: true,
	}, lua.LString(id), lua.LNumber(old), lua.LNumber(new))
}

func (e *Engine) DeltaUnsigned(id string, old, new uint64) {
	if e.deltaUnsigned == nil {
		return
	}

	_ = e.L.CallByParam(lua.P{
		Fn:   e.deltaUnsigned,
		NRet: 0,
	}, lua.LString(id), lua.LNumber(old), lua.LNumber(new))
}

func (e *Engine) RisingSigned(id string, old, new int64) {
	if e.risingSigned == nil {
		return
	}
	_ = e.L.CallByParam(lua.P{
		Fn:      e.risingSigned,
		NRet:    0,
		Protect: true,
	}, lua.LString(id), lua.LNumber(old), lua.LNumber(new))
}

func (e *Engine) FallingSigned(id string, old, new int64) {
	if e.fallingSigned == nil {
		return
	}
	_ = e.L.CallByParam(lua.P{
		Fn:      e.fallingSigned,
		NRet:    0,
		Protect: true,
	}, lua.LString(id), lua.LNumber(old), lua.LNumber(new))
}

func (e *Engine) RisingUnsigned(id string, old, new uint64) {
	if e.risingUnsigned == nil {
		return
	}
	_ = e.L.CallByParam(lua.P{
		Fn:      e.risingUnsigned,
		NRet:    0,
		Protect: true,
	}, lua.LString(id), lua.LNumber(old), lua.LNumber(new))
}

func (e *Engine) FallingUnsigned(id string, old, new uint64) {
	if e.fallingUnsigned == nil {
		return
	}
	_ = e.L.CallByParam(lua.P{
		Fn:      e.fallingUnsigned,
		NRet:    0,
		Protect: true,
	}, lua.LString(id), lua.LNumber(old), lua.LNumber(new))
}

func (e *Engine) Edge(id string, new bool) {
	if e.edge == nil {
		return
	}
	var b lua.LValue = lua.LFalse
	if new {
		b = lua.LTrue
	}
	_ = e.L.CallByParam(lua.P{
		Fn:      e.edge,
		NRet:    0,
		Protect: true,
	}, lua.LString(id), b)
}

func (e *Engine) cacheCallbacks() {
	e.risingSigned = asFunc(e.L.GetGlobal("risingSigned"))
	e.fallingSigned = asFunc(e.L.GetGlobal("fallingSigned"))
	e.risingUnsigned = asFunc(e.L.GetGlobal("risingUnsigned"))
	e.fallingUnsigned = asFunc(e.L.GetGlobal("fallingUnsigned"))
	e.edge = asFunc(e.L.GetGlobal("edge"))
	e.deltaSigned = asFunc(e.L.GetGlobal("deltaSigned"))
	e.deltaUnsigned = asFunc(e.L.GetGlobal("deltaUnsigned"))
}

func asFunc(v lua.LValue) *lua.LFunction {
	if v == lua.LNil {
		return nil
	}
	if f, ok := v.(*lua.LFunction); ok {
		return f
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
