package main

import (
	"fmt"
	"image"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"
	"github.com/fjl/discv5-streams/fileserver"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type transfersUI struct {
	theme *material.Theme
	tc    *transfersController

	error    *errorMessageUI
	dlButton widget.Clickable
	dlIcon   *widget.Icon
	list     widget.List
	sheet    *downloadSheet
}

func newTransfersUI(theme *material.Theme, tc *transfersController) *transfersUI {
	dlIcon, _ := widget.NewIcon(icons.FileFileDownload)
	ui := &transfersUI{
		tc:     tc,
		theme:  theme,
		dlIcon: dlIcon,
		error:  newErrorMessageUI(theme, tc.RetryLoad, tc.ResetState),
	}
	ui.list.Axis = layout.Vertical
	return ui
}

func (ui *transfersUI) AppBarTitle() string {
	return "Transfers"
}

func (ui *transfersUI) Deactivate() {
	if ui.sheet != nil {
		ui.sheet.close()
	}
}

func (ui *transfersUI) Changed() <-chan struct{} {
	return ui.tc.Changed()
}

func (ui *transfersUI) Layout(gtx C) D {
	state := ui.tc.State()

	switch {
	case state.loading:
		return layout.Center.Layout(gtx, func(gtx C) D {
			return material.Loader(ui.theme).Layout(gtx)
		})

	case state.loadError != nil:
		return ui.error.Layout(gtx, state.loadError.Error())

	default:
		dim := ui.drawTransferList(gtx, state.list)
		if ui.sheet != nil {
			ui.sheet.Layout(gtx)
			if ui.sheet.isClosed() {
				ui.sheet = nil
			}
		}
		if ui.sheet == nil || ui.sheet.isClosing() {
			layout.UniformInset(unit.Dp(12)).Layout(gtx, ui.drawDownloadButton)
		}
		return dim
	}
}

func (ui *transfersUI) drawTransferList(gtx C, transfers []*transfer) D {
	list := material.List(ui.theme, &ui.list)
	return list.Layout(gtx, len(transfers), func(gtx C, index int) D {
		tx := transfers[index]
		inset := layout.Inset{Top: 8, Bottom: 2, Left: 16, Right: 4}
		if index == 0 {
			inset.Top = 16
		}

		flex := layout.Flex{Axis: layout.Vertical}
		return flex.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return inset.Layout(gtx, func(gtx C) D {
					return ui.drawTransferRow(gtx, tx)
				})
			}),
			layout.Rigid(func(gtx C) D {
				return component.Divider(ui.theme).Layout(gtx)
			}),
		)
	})
}

func (ui *transfersUI) drawTransferRow(gtx C, tx *transfer) D {
	vertical := layout.Flex{Axis: layout.Vertical}
	horizontal := layout.Flex{Axis: layout.Horizontal}

	dim := vertical.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return horizontal.Layout(gtx,
				layout.Flexed(1.0, func(gtx C) D {
					return ui.drawTransferName(gtx, tx)
				}),
				// layout.Rigid(func(gtx C) D {
				//	return ui.drawTransferButtons(gtx, tx)
				// }),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx C) D {
			return ui.drawTransferProgress(gtx, tx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx C) D {
			return ui.drawTransferStatus(gtx, tx)
		}),
	)

	// Expand the row to fill the available width.
	dim.Size.X = gtx.Constraints.Max.X
	return dim
}

func (ui *transfersUI) drawTransferName(gtx C, tx *transfer) D {
	return material.Body1(ui.theme, tx.Name).Layout(gtx)
}

func (ui *transfersUI) drawTransferProgress(gtx C, tx *transfer) D {
	if tx.Status != transferStatusDownloading {
		return D{}
	}
	progress := float32(tx.ReadBytes) / float32(tx.Size)
	return material.ProgressBar(ui.theme, progress).Layout(gtx)
}

func (ui *transfersUI) drawTransferStatus(gtx C, tx *transfer) D {
	var text string
	switch tx.Status {
	case transferStatusDownloading:
		text = fmt.Sprintf("%s / %s (%s/s)", bytesString(tx.ReadBytes), bytesString(tx.Size), bytesString(tx.ReadSpeed))
	case transferStatusError:
		text = fmt.Sprintf("Error: %s", tx.Error)
	case transferStatusDone:
		text = fmt.Sprintf("%s (%s)", bytesString(tx.Size), tx.Created.Format(time.DateTime))
	default:
		text = bytesString(tx.Size)
	}
	return material.Caption(ui.theme, text).Layout(gtx)
}

func (ui *transfersUI) drawDownloadButton(gtx C) D {
	if ui.dlButton.Clicked() && ui.sheet == nil {
		ui.sheet = ui.newDownloadSheet()
	}
	btn := material.IconButton(ui.theme, &ui.dlButton, ui.dlIcon, "Download File")
	return layout.SE.Layout(gtx, btn.Layout)
}

// downloadSheet is the form for entering a file reference.
type downloadSheet struct {
	ui     *transfersUI
	modal  component.ModalState
	input  component.TextField
	submit widget.Clickable
}

func (ui *transfersUI) newDownloadSheet() *downloadSheet {
	s := &downloadSheet{ui: ui}
	s.modal.State = component.Invisible
	s.modal.Duration = 100 * time.Millisecond
	s.modal.Show(time.Now(), s.drawSheet)
	s.input.SingleLine = true
	s.input.Submit = true
	s.input.Focus()
	return s
}

func (s *downloadSheet) close() {
	s.modal.Disappear(time.Now())
}

func (s *downloadSheet) isClosed() bool {
	return s.modal.State == component.Invisible
}

func (s *downloadSheet) isClosing() bool {
	return s.isClosed() || s.modal.State == component.Disappearing
}

func (s *downloadSheet) handleSubmit(text string) (err error) {
	defer func() {
		if err == nil {
			s.input.ClearError()
		} else {
			s.input.SetError("Parse error: " + err.Error())
		}
	}()

	ref, err := fileserver.ParseURL(text)
	if err != nil {
		return err
	}
	s.ui.tc.StartTransfer(ref)
	s.close()
	return nil
}

func (s *downloadSheet) Layout(gtx C) D {
	m := component.Modal(s.ui.theme, &s.modal)
	return m.Layout(gtx)
}

func (s *downloadSheet) drawSheet(gtx C) D {
	inset := layout.Inset{Top: unit.Dp(64)}
	return inset.Layout(gtx, func(gtx C) D {
		// Draw the background.
		rect := image.Rectangle{Max: gtx.Constraints.Max}
		rr := clip.RRect{Rect: rect, NE: 16, NW: 16}
		paint.FillShape(gtx.Ops, s.ui.theme.Bg, rr.Op(gtx.Ops))

		// Add a click area to prevent closing the dialog.
		area := clip.Rect(rect).Push(gtx.Ops)
		defer area.Pop()
		input := pointer.InputOp{Tag: s, Types: pointer.Press}
		input.Add(gtx.Ops)

		// Draw the content.
		inset := layout.UniformInset(unit.Dp(16))
		return inset.Layout(gtx, s.drawForm)
	})
}

func (s *downloadSheet) drawForm(gtx C) D {
	if s.isClosing() {
		gtx = gtx.Disabled()
	}

	if s.submit.Clicked() {
		s.handleSubmit(s.input.Text())
	} else {
		for _, ev := range s.input.Events() {
			switch ev := ev.(type) {
			case widget.SubmitEvent:
				s.handleSubmit(ev.Text)
			}
		}
	}

	flex := layout.Flex{Axis: layout.Vertical}
	return flex.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return s.input.Layout(gtx, s.ui.theme, "File reference...")
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx C) D {
			return layout.NE.Layout(gtx, func(gtx C) D {
				return material.Button(s.ui.theme, &s.submit, "Download").Layout(gtx)
			})
		}),
	)
}
