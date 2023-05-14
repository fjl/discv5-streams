package main

import (
	"eliasnaur.com/font/roboto/robotobold"
	"eliasnaur.com/font/roboto/robotoitalic"
	"eliasnaur.com/font/roboto/robotomedium"
	"eliasnaur.com/font/roboto/robotoregular"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
)

const monospaceTypeface = "Go"

func appFontCollection() []text.FontFace {
	gofont := gofont.Collection()
	for i := range gofont {
		gofont[i].Font.Typeface = "Go"
	}

	roboto := []text.FontFace{
		parseFont(robotoregular.TTF, "Roboto", text.Regular, text.Normal),
		parseFont(robotoitalic.TTF, "Roboto", text.Italic, text.Normal),
		parseFont(robotobold.TTF, "Roboto", text.Regular, text.Bold),
		parseFont(robotomedium.TTF, "Roboto", text.Regular, text.Medium),
	}
	return append(roboto, gofont...)
}

func parseFont(ttf []byte, name string, style text.Style, weight text.Weight) text.FontFace {
	face, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return text.FontFace{
		Font: text.Font{Typeface: text.Typeface(name), Style: style, Weight: weight},
		Face: face,
	}
}
