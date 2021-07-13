package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/alexballas/go2tv/internal/devices"
	"github.com/alexballas/go2tv/internal/gui"
	"github.com/alexballas/go2tv/internal/httphandlers"
	"github.com/alexballas/go2tv/internal/interactive"
	"github.com/alexballas/go2tv/internal/iptools"
	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/pkg/errors"
)

var (
	version    string
	build      string
	dmrURL     string
	videoArg   = flag.String("v", "", "Path to the video file. (Triggers the CLI mode)")
	subsArg    = flag.String("s", "", "Path to the subtitles file.")
	listPtr    = flag.Bool("l", false, "List all available UPnP/DLNA MediaRenderer models and URLs.")
	targetPtr  = flag.String("t", "", "Cast to a specific UPnP/DLNA MediaRenderer URL.")
	versionPtr = flag.Bool("version", false, "Print version.")
)

func main() {
	guiEnabled := true

	flag.Parse()

	exit, err := checkflags()
	check(err)
	if exit {
		os.Exit(0)
	}
	if *videoArg != "" {
		guiEnabled = false
	}

	if guiEnabled {
		scr := gui.InitFyneNewScreen()
		gui.Start(scr)
	}

	absVideoFile, err := filepath.Abs(*videoArg)
	check(err)

	absSubtitlesFile, err := filepath.Abs(*subsArg)
	check(err)

	transportURL, controlURL, err := soapcalls.DMRextractor(dmrURL)
	check(err)

	whereToListen, err := iptools.URLtoListenIPandPort(dmrURL)
	check(err)

	scr, err := interactive.InitTcellNewScreen()
	check(err)

	// The String() method of the net/url package will properly escape the URL
	// compared to the url.QueryEscape() method.
	videoFileURLencoded := &url.URL{Path: filepath.Base(absVideoFile)}
	subsFileURLencoded := &url.URL{Path: filepath.Base(absSubtitlesFile)}

	tvdata := &soapcalls.TVPayload{
		TransportURL:  transportURL,
		ControlURL:    controlURL,
		CallbackURL:   "http://" + whereToListen + "/callback",
		VideoURL:      "http://" + whereToListen + "/" + videoFileURLencoded.String(),
		SubtitlesURL:  "http://" + whereToListen + "/" + subsFileURLencoded.String(),
		CurrentTimers: make(map[string]*time.Timer),
	}

	s := httphandlers.NewServer(whereToListen)
	serverStarted := make(chan struct{})

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		err := s.ServeFiles(serverStarted, absVideoFile, absSubtitlesFile, tvdata, scr)
		check(err)
	}()
	// Wait for HTTP server to properly initialize
	<-serverStarted

	err = tvdata.SendtoTV("Play1")
	check(err)

	scr.InterInit(*tvdata)
}

func check(err error) {
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
}

func listFlagFunction() error {
	if len(devices.Devices) == 0 {
		return errors.New("-l and -t can't be used together")
	}
	fmt.Println()

	// We loop through this map twice as we need to maintain
	// the correct order.
	keys := make([]int, 0)
	for k := range devices.Devices {
		keys = append(keys, k)
	}

	sort.Ints(keys)

	for _, k := range keys {
		boldStart := ""
		boldEnd := ""

		if runtime.GOOS == "linux" {
			boldStart = "\033[1m"
			boldEnd = "\033[0m"
		}
		fmt.Printf("%sDevice %v%s\n", boldStart, k, boldEnd)
		fmt.Printf("%s--------%s\n", boldStart, boldEnd)
		fmt.Printf("%sModel:%s %s\n", boldStart, boldEnd, devices.Devices[k][0])
		fmt.Printf("%sURL:%s   %s\n", boldStart, boldEnd, devices.Devices[k][1])
		fmt.Println()
	}

	return nil
}

func checkflags() (exit bool, err error) {
	checkVerflag()

	if checkGUI() {
		return false, nil
	}

	if err := checkTflag(); err != nil {
		return false, errors.Wrap(err, "checkflags error")
	}

	list, err := checkLflag()
	if err != nil {
		return false, errors.Wrap(err, "checkflags error")
	}

	if err := checkVflag(); err != nil {
		return false, errors.Wrap(err, "checkflags error")
	}

	if list {
		return true, nil
	}

	if err := checkSflag(); err != nil {
		return false, errors.Wrap(err, "checkflags error")
	}

	return false, nil
}

func checkVflag() error {
	if !*listPtr {
		if _, err := os.Stat(*videoArg); os.IsNotExist(err) {
			return errors.Wrap(err, "checkVflag error")
		}
	}

	return nil
}

func checkSflag() error {
	if *subsArg != "" {
		if _, err := os.Stat(*subsArg); os.IsNotExist(err) {
			return errors.Wrap(err, "checkSflag error")
		}
	} else {
		// The checkVflag should happen before
		// checkSflag so we're safe to call *videoArg
		// here. If *subsArg is empty, try to
		// automatically find the srt from the
		// video filename.
		*subsArg = (*videoArg)[0:len(*videoArg)-
			len(filepath.Ext(*videoArg))] + ".srt"
	}

	return nil
}

func checkTflag() error {
	if *targetPtr == "" {
		err := devices.LoadSSDPservices(1)
		if err != nil {
			return errors.Wrap(err, "checkTflag service loading error")
		}

		dmrURL, err = devices.DevicePicker(1)
		if err != nil {
			return errors.Wrap(err, "checkTflag device picker error")
		}
	} else {
		// Validate URL before proceeding.
		_, err := url.ParseRequestURI(*targetPtr)
		if err != nil {
			return errors.Wrap(err, "checkTflag parse error")
		}
		dmrURL = *targetPtr
	}

	return nil
}

func checkLflag() (bool, error) {
	if *listPtr {
		if err := listFlagFunction(); err != nil {
			return false, errors.Wrap(err, "checkLflag error")
		}
		return true, nil
	}

	return false, nil
}

func checkVerflag() {
	if *versionPtr {
		fmt.Printf("Go2TV Version: %s, ", version)
		fmt.Printf("Build: %s\n", build)
		os.Exit(0)
	}
}

func checkGUI() bool {
	return *videoArg == "" && !*listPtr
}
