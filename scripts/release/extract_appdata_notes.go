package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

const defaultAppData = "assets/linux/app.go2tv.go2tv.appdata.xml"

type appData struct {
	Releases []release `xml:"releases>release"`
}

type release struct {
	Version     string      `xml:"version,attr"`
	Date        string      `xml:"date,attr"`
	Description description `xml:"description"`
	URLs        []urlEntry  `xml:"url"`
}

type description struct {
	Items []string `xml:"ul>li"`
}

type urlEntry struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/release/extract_appdata_notes.go <version> [appdata-file]")
		os.Exit(1)
	}

	version := strings.TrimPrefix(os.Args[1], "v")
	appDataPath := defaultAppData
	if len(os.Args) == 3 {
		appDataPath = os.Args[2]
	}

	data, err := os.ReadFile(appDataPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read appdata: %v\n", err)
		os.Exit(1)
	}

	var parsed appData
	if err := xml.Unmarshal(data, &parsed); err != nil {
		fmt.Fprintf(os.Stderr, "parse appdata xml: %v\n", err)
		os.Exit(1)
	}

	var target *release
	for i := range parsed.Releases {
		if parsed.Releases[i].Version == version {
			target = &parsed.Releases[i]
			break
		}
	}

	if target == nil {
		fmt.Fprintf(os.Stderr, "release %s not found in %s\n", version, appDataPath)
		os.Exit(1)
	}

	fmt.Printf("## Changelog (%s)\n\n", target.Date)
	for _, item := range target.Description.Items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		fmt.Printf("- %s\n", trimmed)
	}

	for _, u := range target.URLs {
		if u.Type == "details" {
			link := strings.TrimSpace(u.Value)
			if link != "" {
				fmt.Printf("\nDetails: %s\n", link)
			}
			break
		}
	}
}
