package main

import (
	"FactFinder/emulator"
	"FactFinder/processing"
	"FactFinder/repo"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type ConnectionState struct {
	ConnectionStatus emulator.ConnectionStatus `json:"connection_status"`
	Message          string                    `json:"message"`
}

// App struct
type App struct {
	ctx              context.Context
	factFinderFolder string
	readPlan         *emulator.ReadPlan
	memoryReader     emulator.MemoryReader
	processingEngine *processing.Engine
	osConnectionCh   chan bool
}

// NewApp creates a new App application struct
func NewApp(factFinderFolder string,
	memoryReader emulator.MemoryReader,
	processingEngine *processing.Engine,
	osConnectionCh chan bool) *App {

	return &App{
		factFinderFolder: factFinderFolder,
		memoryReader:     memoryReader,
		processingEngine: processingEngine,
		osConnectionCh:   osConnectionCh,
	}
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

	go func() {
		err := a.StartEmulatorClient()
		if err != nil {
			fmt.Println(err)
			return
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

func (a *App) OpenFactProviderFolder() {
	runtime.BrowserOpenURL(a.ctx, a.factFinderFolder)
}

func (a *App) SetReadPlan(path string) error {
	f, err := os.Open(filepath.Join(path, "readplan.yml"))
	if err != nil {
		return err
	}
	defer f.Close()

	a.readPlan, err = emulator.NewReadPlan(f)
	if err != nil {
		return err
	}

	luaFile := filepath.Join(path, "factbuilder.lua")
	err = a.processingEngine.LoadFile(luaFile)
	if err != nil {
		return err
	}

	return nil
}

func (a *App) StartEmulatorClient() error {
	connectionStatus := ConnectionState{
		ConnectionStatus: emulator.Disconnected,
		Message:          "Looking for Emulator",
	}

	if a.memoryReader == nil {
		return fmt.Errorf("emulator memory reader is nil")
	}

	// Connect to emulator
	fmt.Println("starting memory reader")
	for {
		if status := a.memoryReader.ConnectEmulator(); status != emulator.Connected {
			fmt.Println("retying emulator connection")
			time.Sleep(1 * time.Second)
			continue
		}

		break
	}

	fmt.Println("emulator initial connect")

	go func() {
		for {
			// If we're connected but don't have a loaded read plan don't bother trying to read values,
			// just inform the UI that we're waiting on something and start over.
			if a.readPlan == nil {
				connectionStatus.ConnectionStatus = emulator.WaitingForGame
				connectionStatus.Message = "Select a Fact Provider"
				runtime.EventsEmit(a.ctx, "emulator:connection", connectionStatus)
				time.Sleep(250 * time.Millisecond)
				continue
			}

			break
		}

		interval := time.Duration(a.readPlan.ReadInterval) * time.Millisecond
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			// If we're not connected inform the UI that we are reconnecting, try to reconnect,
			// if we fail sleep for a second and try again
			if a.memoryReader.EmulatorConnected() != emulator.Connected {
				connectionStatus.ConnectionStatus = emulator.Reconnecting
				connectionStatus.Message = "Reconnecting to emulator..."

				runtime.EventsEmit(a.ctx, "emulator:connection", connectionStatus)
				fmt.Println("emulator is not connected, attempting reconnect")
				if status := a.memoryReader.ConnectEmulator(); status != emulator.Connected {
					fmt.Println("failed to reconnect to emulator")
					time.Sleep(1 * time.Second)
					continue
				}

				// we've successfully reconnected, inform the UI and move along
				fmt.Println("reconnected to emulator")
			}

			// With a clean connection, and a loaded read plan, we can now try to get values
			values, err := a.memoryReader.GetValues(a.readPlan)
			if err != nil {
				// The emulator is connected, but a game is not loaded
				//(e.g. RetroArch will return -1 on READ_CORE_MEMORY if it's running, but no game is loaded)
				// Inform the UI that we are waiting on something.
				if errors.Is(err, emulator.GameNotLoadedError) {
					connectionStatus.ConnectionStatus = emulator.WaitingForGame
					connectionStatus.Message = "Game not loaded"

					runtime.EventsEmit(a.ctx, "emulator:connection", connectionStatus)
					continue
				}

				// Otherwise dump the error to log and continue
				fmt.Println(err)
				continue
			}

			connectionStatus.ConnectionStatus = emulator.Connected
			connectionStatus.Message = "Emulator connected"
			runtime.EventsEmit(a.ctx, "emulator:connection", connectionStatus)

			err = a.processingEngine.ProcessValues(a.readPlan, values)
			if err != nil {
				fmt.Println(err)
				continue
			}

		}
	}()

	return nil
}
