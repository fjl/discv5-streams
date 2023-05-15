package main

import (
	"image/color"
	"log"
	"path/filepath"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"
	"gioui.org/x/explorer"
	"github.com/fjl/discv5-streams/host"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type appState struct {
	net       *networkController
	fs        *filesController
	transfers *transfersController
}

func newAppState(dataDir string, config host.Config) *appState {
	const appName = "discv5-fileshare"
	fileSpaceFile := filepath.Join(dataDir, appName, "fileSpace.gob")
	transfersFile := filepath.Join(dataDir, appName, "transfers.gob")
	networkDir := filepath.Join(dataDir, appName, "network")
	config.NodeDB = filepath.Join(networkDir, "nodes")

	files := newFilesController(fileSpaceFile)
	net := newNetworkController(networkDir, &config, files.ServeFile)
	st := &appState{
		net:       net,
		fs:        files,
		transfers: newTransfersController(net, transfersFile),
	}
	return st
}

func (st *appState) Close() {
	st.net.Close()
	st.fs.Close()
	st.transfers.Close()
}

type mainUI struct {
	theme  *material.Theme
	appbar *component.AppBar
	modal  *component.ModalLayer
	popup  *popupNotifier
	state  *appState

	filespace      *filesUI
	filespaceIcon  *widget.Icon
	filespaceClick widget.Clickable

	network      *networkUI
	networkIcon  *widget.Icon
	networkClick widget.Clickable

	transfers      *transfersUI
	transfersIcon  *widget.Icon
	transfersClick widget.Clickable

	// This is the 'current view' of the app.
	current appView
}

type appView interface {
	AppBarTitle() string
	AppBarActions() []*appMenuItem

	Layout(C) D  // draws the view
	Deactivate() // called when switching to another view

	// This channel should be notified when state has changed.
	Changed() <-chan struct{}
}

type appMenuItem struct {
	Name   string
	Action func()
}

func newMainUI(th *material.Theme, exp *explorer.Explorer, state *appState) *mainUI {
	ui := &mainUI{
		state: state,
		theme: th,
	}
	ui.filespaceIcon, _ = widget.NewIcon(icons.ContentInbox)
	ui.networkIcon, _ = widget.NewIcon(icons.DeviceWiFiTethering)
	ui.transfersIcon, _ = widget.NewIcon(icons.NotificationSync)

	ui.popup = newPopupNotifier(th)
	ui.filespace = newFilesUI(th, exp, ui.popup, state.fs, state.net)
	ui.network = newNetworkUI(th, ui.popup, state.net)
	ui.transfers = newTransfersUI(th, state.transfers)

	ui.modal = component.NewModal()
	ui.appbar = component.NewAppBar(ui.modal)
	ui.changeView(ui.filespace)
	return ui
}

func (ui *mainUI) changeView(view appView) {
	if ui.current != nil {
		ui.current.Deactivate()
	}
	ui.current = view
	ui.appbar.Title = view.AppBarTitle()
	ui.appbar.SetActions(ui.actions())
}

func (ui *mainUI) actions() ([]component.AppBarAction, []component.OverflowAction) {
	filespaceOverflow := component.OverflowAction{Name: "Files", Tag: viewChange{ui.filespace}}
	networkOverflow := component.OverflowAction{Name: "Network", Tag: viewChange{ui.network}}
	transfersOverflow := component.OverflowAction{Name: "Transfers", Tag: viewChange{ui.transfers}}
	actions := []component.AppBarAction{
		{
			OverflowAction: networkOverflow,
			Layout: func(gtx C, bg, fg color.NRGBA) D {
				a := component.SimpleIconAction(&ui.networkClick, ui.networkIcon, networkOverflow)
				return a.Layout(gtx, bg, fg)
			},
		},
		{
			OverflowAction: transfersOverflow,
			Layout: func(gtx C, bg, fg color.NRGBA) D {
				a := component.SimpleIconAction(&ui.transfersClick, ui.transfersIcon, transfersOverflow)
				return a.Layout(gtx, bg, fg)
			},
		},
		{
			OverflowAction: filespaceOverflow,
			Layout: func(gtx C, bg, fg color.NRGBA) D {
				a := component.SimpleIconAction(&ui.filespaceClick, ui.filespaceIcon, filespaceOverflow)
				return a.Layout(gtx, bg, fg)
			},
		},
	}

	// Create the overflow menu.
	menu := ui.current.AppBarActions()
	overflow := make([]component.OverflowAction, len(menu))
	for i := range menu {
		overflow[i] = component.OverflowAction{Name: menu[i].Name, Tag: menu[i]}
	}

	return actions, overflow
}

type viewChange struct {
	view appView
}

func (ui *mainUI) Layout(gtx C) D {
	// Handle AppBar events.
	if ui.filespaceClick.Clicked() {
		ui.changeView(ui.filespace)
	}
	if ui.networkClick.Clicked() {
		ui.changeView(ui.network)
	}
	if ui.transfersClick.Clicked() {
		ui.changeView(ui.transfers)
	}
	for _, ev := range ui.appbar.Events(gtx) {
		switch ev := ev.(type) {
		case component.AppBarOverflowActionClicked:
			switch tag := ev.Tag.(type) {
			case viewChange:
				ui.changeView(tag.view)
			case *appMenuItem:
				log.Printf("main: app menu action %q", tag.Name)
				tag.Action()
			}
		}
	}

	// Render the current view.
	dim := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			navDesc := ""
			overflowDesc := ""
			return ui.appbar.Layout(gtx, ui.theme, navDesc, overflowDesc)
		}),
		layout.Flexed(1, func(gtx C) D {
			return ui.current.Layout(gtx)
		}),
	)

	// Show overlays.
	ui.modal.Layout(gtx, ui.theme)
	ui.popup.Layout(gtx)

	return dim
}
