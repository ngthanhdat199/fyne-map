package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/x/fyne/widget"
)

func main() {
	myApp := app.New()
	m := widget.NewMapWithOptions(
		widget.WithZoomButtons(false),
	)
	m.Zoom(6.0)
	myWindow := myApp.NewWindow("Fyne Map Example")
	myWindow.SetContent(container.NewStack(m))
	myWindow.Resize(fyne.NewSize(600, 400)) // Adjust width and height as needed
	myWindow.CenterOnScreen()
	myWindow.ShowAndRun()
}
