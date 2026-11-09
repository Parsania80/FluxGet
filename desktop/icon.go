package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

const applicationID = "io.github.parsania80.fluxget"

//go:embed assets/fluxget.png
var appIconData []byte

var appIcon = fyne.NewStaticResource("fluxget.png", appIconData)
