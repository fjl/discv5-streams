package main

import (
	"fmt"
	"image"
	"log"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/skip2/go-qrcode"
)

type networkUI struct {
	theme *material.Theme
	popup *popupNotifier
	net   *networkController

	error *errorMessageUI
	list  widget.List
	stats []networkStatUI

	qrcode      image.Image
	qrcodeOp    paint.ImageOp
	qrcodeClick widget.Clickable
	renderedSeq uint64
}

type networkStatUI struct {
	name   string
	render func(s *networkStats) string
	sel    widget.Selectable
}

func newNetworkUI(theme *material.Theme, popup *popupNotifier, net *networkController) *networkUI {
	ui := &networkUI{
		theme: theme,
		popup: popup,
		net:   net,
		error: newErrorMessageUI(theme, net.Restart, nil),
	}
	// Setup field renderers.
	ui.stats = []networkStatUI{
		{
			name: "NodeID",
			render: func(s *networkStats) string {
				return s.LocalENR.ID().String()[:16]
			},
		},
		{
			name: "Address",
			render: func(s *networkStats) string {
				return fmt.Sprintf("%s:%d", s.LocalENR.IP(), s.LocalENR.UDP())
			},
		},
		{
			name: "Peers",
			render: func(s *networkStats) string {
				return fmt.Sprintf("%d", s.TableNodes)
			},
		},
	}
	return ui
}

func (ui *networkUI) AppBarTitle() string {
	return "Network"
}

func (ui *networkUI) AppBarActions() []*appMenuItem {
	return []*appMenuItem{
		{
			Name:   "Restart networking",
			Action: ui.net.Restart,
		},
	}
}

func (ui *networkUI) Changed() <-chan struct{} {
	return ui.net.Changed()
}

func (ui *networkUI) Deactivate() {
}

func (ui *networkUI) Layout(gtx C) D {
	state := ui.net.State()
	switch {
	case state.loading:
		return layout.Center.Layout(gtx, func(gtx C) D {
			return material.Loader(ui.theme).Layout(gtx)
		})

	case state.startError != nil:
		return ui.error.Layout(gtx, state.startError.Error())

	default:
		return ui.drawStats(gtx, state.stats)
	}
}

func (ui *networkUI) drawStats(gtx C, stats networkStats) D {
	node := stats.LocalENR
	ui.list.Axis = layout.Vertical
	list := material.List(ui.theme, &ui.list)
	inset := layout.UniformInset(unit.Dp(16))
	inset.Top = unit.Dp(8)

	return list.Layout(gtx, len(ui.stats)+2, func(gtx C, i int) D {
		switch i {
		// QR code
		case 0:
			return inset.Layout(gtx, func(gtx C) D {
				return ui.drawENR(gtx, node)
			})
		// Divider
		case 1:
			return inset.Layout(gtx, func(gtx C) D {
				return component.Divider(ui.theme).Layout(gtx)
			})
		// Stats
		default:
			i -= 2
			stat := &ui.stats[i]
			return inset.Layout(gtx, func(gtx C) D {
				return ui.drawStat(gtx, stat)
			})
		}
	})
}

func (ui *networkUI) drawENR(gtx C, node *enode.Node) D {
	flex := layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}
	return flex.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return layout.Center.Layout(gtx, func(gtx C) D {
				return ui.qrcodeClick.Layout(gtx, func(gtx C) D {
					return ui.drawQRCode(gtx, node)
				})
			})
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Spacer{Height: unit.Dp(4)}.Layout(gtx)
		}),
		layout.Rigid(func(gtx C) D {
			text := "This is your node record. Share it with others to connect. Click the QR code to copy as text."
			return layout.Center.Layout(gtx, func(gtx C) D {
				return material.Caption(ui.theme, text).Layout(gtx)
			})
		}),
	)
}

func (ui *networkUI) drawQRCode(gtx C, node *enode.Node) D {
	if ui.qrcodeClick.Clicked() {
		clipboard.WriteOp{Text: node.String()}.Add(gtx.Ops)
		ui.popup.ShowNotification("ENR copied to clipboard.")
	}

	sizePx := gtx.Dp(unit.Dp(256))
	if gtx.Constraints.Max.X < sizePx {
		sizePx = gtx.Constraints.Max.X
	}

	gtx.Constraints = layout.Exact(image.Pt(sizePx, sizePx))
	ui.renderENR(node, sizePx)
	ui.qrcodeOp.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	return D{Size: gtx.Constraints.Max}
}

func (ui *networkUI) renderENR(node *enode.Node, size int) {
	if ui.qrcode != nil && ui.qrcode.Bounds().Dx() == size && ui.renderedSeq == node.Seq() {
		return // no changes
	}
	log.Printf("network: rendering ENR seq=%d size=%d", node.Seq(), size)
	qr, _ := qrcode.New(node.String(), qrcode.Medium)
	ui.qrcode = qr.Image(size)
	ui.qrcodeOp = paint.NewImageOp(ui.qrcode)
	ui.renderedSeq = node.Seq()
}

func (ui *networkUI) drawStat(gtx C, stat *networkStatUI) D {
	flex := layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}
	return flex.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return material.Body1(ui.theme, stat.name).Layout(gtx)
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Spacer{Width: 4}.Layout(gtx)
		}),
		layout.Rigid(func(gtx C) D {
			value := stat.render(&ui.net.State().stats)
			label := material.Body1(ui.theme, value)
			label.Font.Typeface = "Go"
			label.Font.Variant = "Mono"
			label.State = &stat.sel
			return label.Layout(gtx)
		}),
	)
}
