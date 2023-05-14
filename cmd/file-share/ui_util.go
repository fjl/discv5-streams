package main

import (
	"image"
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type C = layout.Context
type D = layout.Dimensions

type errorMessageUI struct {
	theme *material.Theme
	icon  *widget.Icon

	retryFunc func()
	resetFunc func()
	retryText string
	resetText string

	buttons     []layout.FlexChild
	retryClick  widget.Clickable
	retryButton material.ButtonStyle
	resetClick  widget.Clickable
	resetButton material.ButtonStyle
}

func newErrorMessageUI(theme *material.Theme, retry, reset func()) *errorMessageUI {
	errorIcon, _ := widget.NewIcon(icons.AlertError)
	e := &errorMessageUI{
		theme:     theme,
		icon:      errorIcon,
		retryFunc: retry,
		resetFunc: reset,
		retryText: "Retry",
		resetText: "Reset Database",
		buttons:   make([]layout.FlexChild, 0, 3),
	}

	e.retryButton = material.Button(e.theme, &e.retryClick, e.retryText)
	e.resetButton = material.Button(e.theme, &e.resetClick, e.resetText)
	e.resetButton.Color = e.theme.Palette.Fg
	e.resetButton.Background = e.theme.Palette.Bg
	if retry != nil {
		e.buttons = append(e.buttons, layout.Rigid(e.retryButton.Layout))
	}
	if reset != nil {
		e.buttons = append(e.buttons, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout))
		e.buttons = append(e.buttons, layout.Rigid(e.resetButton.Layout))
	}

	return e
}

func (e *errorMessageUI) Layout(gtx C, errMsg string) D {
	errLabel := material.Body1(e.theme, errMsg)

	return layout.Center.Layout(gtx, func(gtx C) D {
		flex := layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}
		return flex.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				size := gtx.Dp(96)
				gtx.Constraints = layout.Exact(image.Pt(size, size))
				return e.icon.Layout(gtx, e.theme.Palette.Fg)
			}),
			layout.Rigid(errLabel.Layout),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(e.drawButtons),
		)
	})
}

// drawButtons shows the retry and reset buttons under the error message.
func (e *errorMessageUI) drawButtons(gtx C) D {
	if e.retryFunc != nil && e.retryClick.Clicked() {
		e.retryFunc()
	}
	if e.resetFunc != nil && e.resetClick.Clicked() {
		e.resetFunc()
	}
	flex := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}
	return flex.Layout(gtx, e.buttons...)
}

// popupNotifier shows unobtrusive notifications at the bottom of the screen.
type popupNotifier struct {
	theme *material.Theme
	cur   *popupNotification
	next  *popupNotification
	vis   component.VisibilityAnimation
}

type popupNotification struct {
	text         string
	disappear    time.Time
	remove       time.Time
	disappearing bool
}

var white = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
var black = color.NRGBA{0x00, 0x00, 0x00, 0xFF}
var gray = color.NRGBA{0xEE, 0xEE, 0xEE, 0xFF}

const (
	notificationDuration  = 2 * time.Second
	notificationAppear    = 100 * time.Millisecond
	notificationDisappear = 250 * time.Millisecond
)

func newPopupNotifier(th *material.Theme) *popupNotifier {
	theme := th.WithPalette(material.Palette{
		Fg:         th.Palette.ContrastFg,
		Bg:         th.Palette.ContrastBg,
		ContrastBg: gray,
		ContrastFg: black,
	})
	n := &popupNotifier{theme: &theme}
	return n
}

func (n *popupNotifier) ShowNotification(text string) {
	nf := &popupNotification{text: text}
	now := time.Now()
	showTime := now
	if n.cur == nil {
		n.cur = nf
		n.startAppearAnimation(now)
	} else {
		n.next = nf
		n.startReplaceAnimation(now)
		showTime = n.cur.remove
	}
	nf.disappear = showTime.Add(notificationAppear + notificationDuration)
	nf.remove = nf.disappear.Add(notificationDisappear)
}

func (n *popupNotifier) startAppearAnimation(now time.Time) {
	n.vis = component.VisibilityAnimation{
		Duration: notificationAppear,
		State:    component.Invisible,
	}
	n.vis.Appear(now)
}

func (n *popupNotifier) startDisappearAnimation(now time.Time) {
	n.vis = component.VisibilityAnimation{
		Duration: notificationDisappear,
	}
	n.vis.Disappear(now)
}

func (n *popupNotifier) startReplaceAnimation(now time.Time) {
	d := notificationDisappear / 4
	n.vis.Duration = d
	n.vis.Disappear(now)
	n.cur.disappear = now
	n.cur.remove = now.Add(d)
	n.cur.disappearing = true
}

func (n *popupNotifier) Layout(gtx C) D {
	if n.cur == nil {
		return D{}
	}

	// Schedule disappear/remove/replace.
	now := time.Now()
	if now.Before(n.cur.disappear) {
		op.InvalidateOp{At: n.cur.disappear}.Add(gtx.Ops)
	} else if now.Before(n.cur.remove) {
		if !n.cur.disappearing {
			n.startDisappearAnimation(now)
			n.cur.disappearing = true
		}
		op.InvalidateOp{At: n.cur.remove}.Add(gtx.Ops)
	} else {
		if n.next == nil {
			n.cur = nil
			return D{}
		} else {
			// Fade in next scheduled notification.
			n.cur = n.next
			n.next = nil
			n.startAppearAnimation(now)
		}
	}

	// Render the current notification.
	inset := layout.Inset{Top: 4, Left: 4, Right: 4, Bottom: 32}
	return inset.Layout(gtx, func(gtx C) D {
		return layout.S.Layout(gtx, func(gtx C) D {
			gtx.Constraints.Min = image.ZP
			return n.drawNotification(gtx, n.cur)
		})
	})
}

func (n *popupNotifier) drawNotification(gtx C, notification *popupNotification) D {
	visfrac := n.vis.Revealed(gtx)

	m := op.Record(gtx.Ops)
	inset := layout.Inset{Top: 6, Bottom: 6, Left: 8, Right: 8}
	dim := inset.Layout(gtx, func(gtx C) D {
		l := material.Caption(n.theme, notification.text)
		l.Color = n.theme.Fg
		l.Color.A = byte(float32(255) * visfrac)
		l.MaxLines = 1
		return l.Layout(gtx)
	})
	label := m.Stop()

	const cornerRadius = unit.Dp(2)
	bgcolor := n.theme.Bg
	bgcolor.A = byte(float32(255) * visfrac)

	gtx.Constraints.Min = dim.Size
	sh := component.Shadow(cornerRadius, 1*unit.Dp(visfrac))
	sh.Layout(gtx)
	rect := image.Rectangle{Max: dim.Size}
	rr := clip.UniformRRect(rect, gtx.Dp(cornerRadius))
	paint.FillShape(gtx.Ops, bgcolor, rr.Op(gtx.Ops))
	label.Add(gtx.Ops)
	return dim
}
