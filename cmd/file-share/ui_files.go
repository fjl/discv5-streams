package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"
	"gioui.org/x/explorer"
	"github.com/fjl/discv5-streams/fileserver"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type filesUI struct {
	theme *material.Theme
	exp   *explorer.Explorer
	popup *popupNotifier
	net   *networkController
	fs    *filesController

	state fileListForUI

	list             widget.List
	addButton        widget.Clickable
	retryLoadButton  widget.Clickable
	resetStateButton widget.Clickable
	addIcon          *widget.Icon
	shareIcon        *widget.Icon
	removeIcon       *widget.Icon
	error            *errorMessageUI
}

type fileListForUI struct {
	ptr  *filesState
	list []*fileRefForUI
}

type fileRefForUI struct {
	*fileRef
	shareButton  widget.Clickable
	removeButton widget.Clickable
}

// update creates file list items from the current state.
func (list *fileListForUI) update(data *filesState) {
	if data == list.ptr {
		return // no changes
	}
	list.ptr = data
	list.list = make([]*fileRefForUI, len(data.list))
	for i, file := range data.list {
		list.list[i] = &fileRefForUI{fileRef: file}
	}
}

func newFilesUI(th *material.Theme, exp *explorer.Explorer, popup *popupNotifier, fs *filesController, net *networkController) *filesUI {
	addIcon, _ := widget.NewIcon(icons.ContentAdd)
	shareIcon, _ := widget.NewIcon(icons.SocialShare)
	removeIcon, _ := widget.NewIcon(icons.ActionDelete)
	return &filesUI{
		theme:      th,
		fs:         fs,
		net:        net,
		exp:        exp,
		popup:      popup,
		addIcon:    addIcon,
		shareIcon:  shareIcon,
		removeIcon: removeIcon,
		error:      newErrorMessageUI(th, fs.RetryLoad, fs.ResetDatabase),
	}
}

func (ui *filesUI) AppBarTitle() string {
	return "Files"
}

func (ui *filesUI) AppBarActions() []*appMenuItem {
	return []*appMenuItem{
		{
			Name:   "Remove all files",
			Action: ui.fs.ResetDatabase,
		},
	}
}

func (ui *filesUI) Changed() <-chan struct{} {
	return ui.fs.Changed()
}

func (ui *filesUI) Deactivate() {
}

func (ui *filesUI) Layout(gtx C) D {
	state := ui.fs.State()

	switch {
	case state.loading:
		return layout.Center.Layout(gtx, func(gtx C) D {
			return material.Loader(ui.theme).Layout(gtx)
		})

	case state.loadError != nil:
		errMsg := "Error loading database: " + state.loadError.Error()
		return ui.error.Layout(gtx, errMsg)

	default:
		ui.state.update(state)
		dim := ui.drawFileList(gtx)
		layout.UniformInset(unit.Dp(12)).Layout(gtx, ui.drawAddButton)
		return dim
	}
}

// drawFileList shows the fileList.
func (ui *filesUI) drawFileList(gtx C) D {
	ui.list.Axis = layout.Vertical
	ls := material.List(ui.theme, &ui.list)
	return ls.Layout(gtx, len(ui.state.list), func(gtx C, index int) D {
		file := ui.state.list[index]
		inset := layout.Inset{Top: 8, Bottom: 2, Left: 16, Right: 4}
		if index == 0 {
			inset.Top = 16
		}

		flex := layout.Flex{Axis: layout.Vertical}
		return flex.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return inset.Layout(gtx, func(gtx C) D {
					return ui.drawFileRow(gtx, file)
				})
			}),
			layout.Rigid(func(gtx C) D {
				return component.Divider(ui.theme).Layout(gtx)
			}),
		)
	})
}

func (ui *filesUI) drawFileRow(gtx C, file *fileRefForUI) D {
	flex := layout.Flex{Axis: layout.Horizontal}
	dim := flex.Layout(gtx,
		layout.Flexed(1.0, func(gtx C) D {
			return ui.drawFileRowLeft(gtx, file)
		}),
		layout.Rigid(func(gtx C) D {
			return ui.drawFileButtons(gtx, file)
		}),
	)

	// Expand the row to fill the available width.
	dim.Size.X = gtx.Constraints.Max.X
	return dim
}

func (ui *filesUI) drawFileButtons(gtx C, file *fileRefForUI) D {
	if file.removeButton.Clicked() {
		ui.fs.RemoveFile(file.fileRef)
	}
	if file.shareButton.Clicked() {
		ui.doShareFile(gtx, file)
	}

	flex := layout.Flex{Axis: layout.Horizontal}
	return flex.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			fg := ui.theme.Palette.Bg
			bg := ui.theme.Palette.Fg
			btn := component.SimpleIconButton(fg, bg, &file.removeButton, ui.removeIcon)
			btn.Size = unit.Dp(16)
			return btn.Layout(gtx)
		}),
		layout.Rigid(func(gtx C) D {
			fg := ui.theme.Palette.Bg
			bg := ui.theme.Palette.Fg
			btn := component.SimpleIconButton(fg, bg, &file.shareButton, ui.shareIcon)
			btn.Size = unit.Dp(16)
			return btn.Layout(gtx)
		}),
	)
}

func (ui *filesUI) drawFileRowLeft(gtx C, file *fileRefForUI) D {
	flex := layout.Flex{Axis: layout.Vertical}
	return flex.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return material.Body1(ui.theme, file.Name).Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		layout.Rigid(func(gt C) D {
			return material.Caption(ui.theme, bytesString(file.info.Size())).Layout(gtx)
		}),
	)
}

func (ui *filesUI) drawAddButton(gtx C) D {
	if ui.addButton.Clicked() {
		go ui.runAddFile()
	}
	btn := material.IconButton(ui.theme, &ui.addButton, ui.addIcon, "Add File")
	return layout.SE.Layout(gtx, btn.Layout)
}

func (ui *filesUI) doShareFile(gtx C, file *fileRefForUI) {
	netstate := ui.net.State()
	node := netstate.stats.LocalENR
	if node == nil {
		ui.popup.ShowNotification("Network is down, try again later!")
		return
	}
	ref := fileserver.TransferRef{Node: node, File: file.Name}
	clipboard.WriteOp{Text: ref.String()}.Add(gtx.Ops)
	ui.popup.ShowNotification("File reference copied to clipboard.")
}

func (ui *filesUI) runAddFile() {
	file, err := ui.exp.ChooseFile()
	if err != nil {
		log.Println("explorer error:", err)
		return
	}
	files := []io.ReadCloser{file}
	for _, f := range files {
		switch f := f.(type) {
		case *os.File:
			stat, err := f.Stat()
			f.Close()
			if err != nil {
				log.Println("error:", err)
				continue
			}
			ui.fs.AddFile(f.Name(), stat)
		default:
			f.Close()
			log.Printf("explorer file is not *os.File: %v", f)
		}
	}
}

// bytesString returns a human-readable string for the given number of bytes.
func bytesString(size int64) string {
	const unit = 1000
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "kMGTPE"[exp])
}
