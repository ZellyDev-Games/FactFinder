package main

import (
	"FactFinder/emulator/retroarch"
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
	providerFolder, err := repo.SetupPaths()
	if err != nil {
		panic(err)
	}

	_, err = repo.ScanReadPlans()
	if err != nil {
		panic(err)
	}

	raClient := retroarch.NewClient("localhost", "55355")
	engine, osConnCh := processing.NewEngine()

	app := NewApp(providerFolder, raClient, engine, osConnCh)

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "FactFinder",
		Width:  400,
		Height: 350,
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
