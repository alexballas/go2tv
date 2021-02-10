package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
)

func listFlagFunction() error {
	if len(devices) == 0 {
		err := errors.New("-l and -t can't be used together")
		return err
	}
	fmt.Println()

	// We loop through this map twice as we need to maintain
	// the correct order.
	keys := make([]int, 0)
	for k := range devices {
		keys = append(keys, k)
	}

	sort.Ints(keys)

	for _, k := range keys {
		fmt.Printf("\033[1mDevice %v\033[0m\n", k)
		fmt.Printf("\033[1m--------\033[0m\n")
		fmt.Printf("\033[1mModel\033[0m: %s\n", devices[k][0])
		fmt.Printf("\033[1mURL\033[0m:   %s\n", devices[k][1])
		fmt.Println()
	}
	return nil
}

func checkflags() (bool, error) {
	if err := checkTflag(); err != nil {
		return false, err
	}
	list, err := checkLflag()
	if err != nil {
		return false, err
	}
	if list == true {
		return true, nil
	}

	if err := checkVflag(); err != nil {
		return false, err
	}

	if err := checkSflag(); err != nil {
		return false, err
	}
	return false, nil
}

func checkVflag() error {
	if *videoArg == "" {
		err := errors.New("No video file defined")
		return err
	}
	if _, err := os.Stat(*videoArg); os.IsNotExist(err) {
		return err
	}
	return nil
}

func checkSflag() error {
	if *subsArg != "" {
		if _, err := os.Stat(*subsArg); os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func checkTflag() error {
	if *targetPtr == "" {
		err := loadSSDPservices()
		if err != nil {
			return err
		}

		dmrURL, err = devicePicker(1)
		if err != nil {
			return err
		}
	} else {
		// Validate URL before proceeding
		_, err := url.ParseRequestURI(*targetPtr)
		if err != nil {
			return err
		}
		dmrURL = *targetPtr
	}
	return nil
}

func checkLflag() (bool, error) {
	if *listPtr == true {
		if err := listFlagFunction(); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
