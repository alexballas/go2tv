package gui

import (
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/internal/devices"
)

type devType struct {
	name string
	addr string
}

func Start() {
	refreshDevices := time.NewTicker(15 * time.Second)

	list := new(widget.List)

	data := make([]devType, 10)
	go func() {
		data2, err := getDevices(1)
		data = data2
		if err != nil {
			data = nil
		}
		list.Refresh()
	}()

	myApp := app.New()
	w := myApp.NewWindow("Go2TV")

	vfile := widget.NewButton("Video File", func() {
		log.Println("tapped")
	})
	sfile := widget.NewButton("Subtitle File", func() {
		log.Println("tapped")
	})
	sfile.Disable()

	devicelabel := widget.NewLabel("Select Device:")
	list = widget.NewList(
		func() int {
			return len(data)
		},
		func() fyne.CanvasObject {
			return container.NewHBox(widget.NewIcon(theme.NavigateNextIcon()), widget.NewLabel("Template Object"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*fyne.Container).Objects[1].(*widget.Label).SetText(data[i].name)
		})

	content := container.NewVBox(vfile, sfile, devicelabel, list)
	content.Resize(fyne.NewSize(6000, 6000))

	list.OnSelected = func(id widget.ListItemID) {
		fmt.Println(data[id].addr)
	}
	go func() {
		for range refreshDevices.C {
			data2, err := getDevices(5)
			data = data2
			if err != nil {
				data = nil
			}
			fmt.Println(data)
			list.Refresh()
		}
	}()
	w.SetContent(content)
	w.Resize(fyne.NewSize(600, 600))
	w.ShowAndRun()
	os.Exit(1)
}

func getDevices(delay int) (dev []devType, err error) {
	if err := devices.LoadSSDPservices(delay); err != nil {
		return nil, err
	}
	// We loop through this map twice as we need to maintain
	// the correct order.
	keys := make([]int, 0)
	for k := range devices.Devices {
		keys = append(keys, k)
	}

	sort.Ints(keys)

	guiDeviceList := make([]devType, 0)
	for _, k := range keys {
		guiDeviceList = append(guiDeviceList, devType{devices.Devices[k][0], devices.Devices[k][1]})
	}
	return guiDeviceList, nil
}
