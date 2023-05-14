package main

import (
	"image/color"
	"path/filepath"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"
	"gioui.org/x/explorer"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type appState struct {
	net       *networkController
	fs        *filesController
	transfers *transfersController
}

func newAppState(dataDir string) *appState {
	const appName = "discv5-fileshare"
	fileSpaceFile := filepath.Join(dataDir, appName, "fileSpace.gob")
	transfersFile := filepath.Join(dataDir, appName, "transfers.gob")
	networkDir := filepath.Join(dataDir, appName, "network")

	files := newFilesController(fileSpaceFile)
	net := newNetworkController(networkDir, files.ServeFile)
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
	Layout(C) D
	Deactivate()
	Changed() <-chan struct{}
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
	ui.appbar.SetActions(ui.actions())

	ui.changeView(ui.filespace)
	return ui
}

func (ui *mainUI) changeView(view appView) {
	if ui.current != nil {
		ui.current.Deactivate()
	}
	ui.appbar.Title = view.AppBarTitle()
	ui.current = view
}

func (ui *mainUI) actions() ([]component.AppBarAction, []component.OverflowAction) {
	filespaceOverflow := component.OverflowAction{Name: "Server", Tag: ui.filespace}
	networkOverflow := component.OverflowAction{Name: "Network", Tag: ui.network}
	transfersOverflow := component.OverflowAction{Name: "Transfers", Tag: ui.network}
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
	return actions, nil
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
