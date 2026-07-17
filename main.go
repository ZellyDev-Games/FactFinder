package main

import (
	// linuxmem "FactFinder/emulator/linux"
	"FactFinder/emulator/nwa"
	"FactFinder/emulator/qusb2snes"
	"FactFinder/emulator/retroarch"
	"FactFinder/logger"
	"FactFinder/processing"
	"FactFinder/repo"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger.Init()

	paths, err := repo.SetupPaths()
	if err != nil {
		panic(err)
	}

	_, err = repo.ScanReadPlans(paths.ProviderDir)
	if err != nil {
		panic(err)
	}

	raClient := retroarch.NewClient("localhost", "55355")
	nwaClient := nwa.NewClient("localhost", "48879")
	qUSB2SNESClient := qusb2snes.NewClient("localhost", "23074")
	// linuxProcessClient := linuxmem.NewClient()
	engine, osConnCh := processing.NewEngine()

	app := NewApp(
		paths.ProviderDir,
		raClient,
		nwaClient,
		qUSB2SNESClient,
		// linuxProcessClient,
		engine,
		osConnCh,
	)

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "FactFinder",
		Width:  400,
		Height: 450,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
