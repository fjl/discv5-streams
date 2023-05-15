package main

import (
	"flag"
	"fmt"
	"os"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget/material"
	"gioui.org/x/explorer"
	ethlog "github.com/ethereum/go-ethereum/log"
	"github.com/fjl/discv5-streams/host"
)

func main() {
	dataDirFlag := flag.String("datadir", "", "data directory")
	flag.Parse()

	// Resolve data directory.
	var dataDir string
	if *dataDirFlag != "" {
		dataDir = *dataDirFlag
	} else {
		dir, err := app.DataDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		dataDir = dir
	}

	// Set up go-ethereum logging.
	h := ethlog.LvlFilterHandler(ethlog.LvlTrace, ethlog.StreamHandler(os.Stderr, ethlog.TerminalFormat(false)))
	ethlog.Root().SetHandler(h)
	state := newAppState(dataDir, host.Config{})

	var (
		title    = app.Title("FileShare")
		portrait = app.PortraitOrientation.Option()
	)
	go func() {
		w := app.NewWindow(title, portrait)
		if err := loop(w, state); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}

// loop is the main loop of the app.
func loop(w *app.Window, state *appState) error {
	var (
		exp = explorer.NewExplorer(w)
		th  = material.NewTheme(appFontCollection())
		ui  = newMainUI(th, exp, state)
		ops op.Ops
	)
	defer state.Close()

	for {
		select {
		case e := <-w.Events():
			exp.ListenEvents(e)

			switch e := e.(type) {
			case system.DestroyEvent:
				return e.Err
			case system.FrameEvent:
				gtx := layout.NewContext(&ops, e)
				paint.Fill(gtx.Ops, th.Palette.Bg)
				ui.Layout(gtx)
				e.Frame(gtx.Ops)
			}

		// Redraw when app state changes.
		case <-ui.current.Changed():
			w.Invalidate()
		}
	}
}
