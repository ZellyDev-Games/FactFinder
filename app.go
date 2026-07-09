package main

import (
	"FactFinder/emulator"
	// linuxmem "FactFinder/emulator/linux"
	"FactFinder/emulator/nwa"
	"FactFinder/emulator/qusb2snes"
	"FactFinder/emulator/retroarch"
	"FactFinder/logger"
	"FactFinder/processing"
	"FactFinder/repo"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var log = logger.Module("app").SetLevel(logger.DebugLevel)

type ConnectionState struct {
	ConnectionStatus emulator.ConnectionStatus `json:"connection_status"`
	Message          string                    `json:"message"`
}

// App struct
type App struct {
	m          sync.RWMutex
	emulatorWG sync.WaitGroup

	ctx            context.Context
	emulatorCancel context.CancelFunc

	Ready            bool
	factFinderFolder string
	state            [][]string
	osConnectionCh   chan bool

	readPlan     *emulator.ReadPlan
	memoryReader emulator.MemoryReader
	values       []emulator.Value

	processingEngine *processing.Engine

	retroarch *retroarch.Client
	nwa       *nwa.Client
	qusb2snes *qusb2snes.Client
	// linuxProcessClient *linuxmem.Client
}

// NewApp creates a new App application struct
func NewApp(
	factFinderFolder string,
	retroarchClient *retroarch.Client,
	nwaClient *nwa.Client,
	qusb2snesClient *qusb2snes.Client,
	// linuxProcessClient *linuxmem.Client,
	processingEngine *processing.Engine,
	osConnectionCh chan bool,
) *App {

	return &App{
		factFinderFolder: factFinderFolder,
		retroarch:        retroarchClient,
		nwa:              nwaClient,
		qusb2snes:        qusb2snesClient,
		// linuxProcessClient: linuxProcessClient,

		// default client
		memoryReader: retroarchClient,

		processingEngine: processingEngine,
		osConnectionCh:   osConnectionCh,
	}
}

func (a *App) SetEmulatorClient(client string) error {
	log.Info("switching emulator -> %s", client)

	// Stop existing worker
	a.m.Lock()
	if a.emulatorCancel != nil {
		a.emulatorCancel()
	}
	a.m.Unlock()

	a.emulatorWG.Wait()

	// Close old connection
	a.m.Lock()
	if a.memoryReader != nil {
		_ = a.memoryReader.Close()
	}

	switch client {
	case "retroarch":
		a.memoryReader = a.retroarch

	case "nwa":
		a.memoryReader = a.nwa

	case "qusb2snes":
		a.memoryReader = a.qusb2snes

	// case "linuxmem":
	// 	a.memoryReader = a.linuxProcessClient

	default:
		a.m.Unlock()
		return fmt.Errorf("unknown emulator client: %s", client)
	}

	a.m.Unlock()

	// Start new worker
	a.emulatorWG.Add(1)

	go func() {
		defer a.emulatorWG.Done()

		if err := a.StartEmulatorClient(); err != nil {
			log.Error("emulator worker exited: %v", err)
		}
	}()

	log.Info("switched emulator client to %s", client)

	return nil
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go func() {
		s := ConnectionState{}
		for {
			status, ok := <-a.osConnectionCh
			if !ok {
				return
			}

			s.ConnectionStatus = emulator.Disconnected
			s.Message = "OpenSplit Not Found"
			if status {
				s.ConnectionStatus = emulator.Connected
				s.Message = "OpenSplit Connected"
			}

			runtime.EventsEmit(a.ctx, "opensplit:connection", s)
		}
	}()

	a.emulatorWG.Add(1)

	go func() {
		defer a.emulatorWG.Done()

		if err := a.StartEmulatorClient(); err != nil {
			log.Error("failed to start emulator client: %v", err)
		}
	}()
}

func (a *App) GetFactProviders() ([]repo.Provider, error) {
	plans, err := repo.ScanReadPlans()
	if err != nil {
		return nil, err
	}

	return plans, nil
}

// func (a *App) OpenFactProviderFolder() {
func (a *App) OpenFactProviderFolder() error {
	switch goruntime.GOOS {
	case "windows":
		return exec.Command("explorer", a.factFinderFolder).Start()

	case "darwin":
		return exec.Command("open", a.factFinderFolder).Start()

	default: // linux
		return exec.Command("xdg-open", a.factFinderFolder).Start()
	}
}

func (a *App) SetReadPlan(path string) error {
	f, err := os.Open(filepath.Join(path, "readplan.yml"))
	if err != nil {
		return err
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	log.Info("loaded read plan from %s", path)

	a.readPlan, err = emulator.NewReadPlan(f)
	if err != nil {
		return err
	}

	// rp := a.readPlan

	// if linux, ok := a.memoryReader.(*linuxmem.Client); ok {
	// 	linux.SetReadPlan(rp)
	// }

	luaFile := filepath.Join(path, "factbuilder.lua")
	err = a.processingEngine.LoadFile(luaFile, a.readPlan)

	if err != nil {
		log.Error("failed to load lua file: %v", err)
		return err
	}

	log.Info("loaded lua factbuilder: %s", luaFile)

	return nil
}

func (a *App) sendState() {
	runtime.EventsEmit(a.ctx, "emulator:state", a.state)
}

func (a *App) sendValues() {
	out := make([][]string, len(a.values))
	for i, v := range a.values {
		stringKey := v.Name
		stringVal := ""
		switch v.Type {
		case emulator.U8, emulator.U16, emulator.U32, emulator.U64:
			stringVal = strconv.FormatUint(v.Unsigned, 16)
		case emulator.I8, emulator.I16, emulator.I32, emulator.I64:
			stringVal = strconv.FormatUint(v.Unsigned, 16)
		case emulator.F32:
			stringVal = strconv.FormatFloat(float64(v.Float32), 'f', -1, 32)
		case emulator.F64:
			stringVal = strconv.FormatFloat(float64(v.Float64), 'f', -1, 64)
		case emulator.String:
			stringVal = v.String
		case emulator.Bool:
			stringVal = strconv.FormatBool(v.Bool)
		case emulator.FlagCount:
			stringVal = strconv.FormatInt(int64(v.FlagCount), 10)
		}
		out[i] = []string{
			stringKey,
			stringVal,
		}
	}

	runtime.EventsEmit(a.ctx, "emulator:values", out)
}

func (a *App) StartEmulatorClient() error {
	log.Info("StartEmulatorClient entered")

	ctx, cancel := context.WithCancel(context.Background())

	a.m.Lock()
	a.emulatorCancel = cancel
	reader := a.memoryReader
	a.m.Unlock()

	if reader == nil {
		return fmt.Errorf("emulator memory reader is nil")
	}

	connectionStatus := ConnectionState{
		ConnectionStatus: emulator.Disconnected,
		Message:          "Looking for Emulator",
	}

	log.Info("starting memory reader")

	// Connect loop (cancelable)
	for {
		select {
		case <-ctx.Done():
			log.Info("emulator worker cancelled during connect")
			return nil

		default:
		}

		if reader.ConnectEmulator() == emulator.Connected {
			break
		}

		log.Warn("retrying emulator connection")

		select {
		case <-ctx.Done():
			return nil

		case <-time.After(time.Second):
		}
	}

	log.Info("emulator connected")

	// Wait for readplan
	for a.readPlan == nil {
		select {
		case <-ctx.Done():
			return nil

		case <-time.After(250 * time.Millisecond):
			connectionStatus.ConnectionStatus = emulator.WaitingForGame
			connectionStatus.Message = "Select a Fact Provider"

			runtime.EventsEmit(
				a.ctx,
				"emulator:connection",
				connectionStatus,
			)
		}
	}

	interval := time.Duration(a.readPlan.ReadInterval) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("emulator worker stopped")
			return nil

		case <-ticker.C:
			if reader.EmulatorConnected() != emulator.Connected {
				connectionStatus.ConnectionStatus = emulator.Reconnecting
				connectionStatus.Message = "Reconnecting to emulator..."

				runtime.EventsEmit(
					a.ctx,
					"emulator:connection",
					connectionStatus,
				)

				log.Warn("emulator disconnected, attempting reconnect")

				if reader.ConnectEmulator() != emulator.Connected {
					log.Error("failed to reconnect to emulator")
					continue
				}

				log.Info("reconnected to emulator")
			}

			compiledReadPlan := reader.CompileReadPlan(a.readPlan)

			values, err := reader.GetValues(compiledReadPlan)
			if err != nil {
				if errors.Is(err, emulator.ErrGameNotLoaded) {
					connectionStatus.ConnectionStatus = emulator.WaitingForGame
					connectionStatus.Message = "Game not loaded"

					runtime.EventsEmit(
						a.ctx,
						"emulator:connection",
						connectionStatus,
					)

					continue
				}

				log.Error("failed to get values: %v", err)
				continue
			}

			connectionStatus.ConnectionStatus = emulator.Connected
			connectionStatus.Message = "Emulator connected"

			runtime.EventsEmit(
				a.ctx,
				"emulator:connection",
				connectionStatus,
			)

			if err := a.processingEngine.ProcessValues(values); err != nil {
				log.Error("processing engine error: %v", err)
				continue
			}

			a.state = a.processingEngine.GetState()
			a.values = values

			a.sendState()
			a.sendValues()
		}
	}
}
