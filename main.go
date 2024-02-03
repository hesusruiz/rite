// Copyright 2023 Jesus Ruiz. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"text/template"
	"time"

	"github.com/hesusruiz/rite/rite"
	"github.com/hesusruiz/rite/sliceedit"
	"github.com/hesusruiz/vcutils/yaml"
	"github.com/urfave/cli/v2"
)

var norespec bool
var debugflag bool

const defaultIndexFileName = "index.rite"

func main() {

	version := "v0.08"

	rtinfo, ok := debug.ReadBuildInfo()
	if ok {
		buildSettings := rtinfo.Settings
		for _, setting := range buildSettings {
			if setting.Key == "vcs.time" {
				version = version + ", built on " + setting.Value
			}
			if setting.Key == "vcs.revision" {
				version = version + ", revision " + setting.Value
			}
		}

	}

	app := &cli.App{
		Name:     "rite",
		Version:  version,
		Compiled: time.Now(),
		Authors: []*cli.Author{
			{
				Name:  "Jesus Ruiz",
				Email: "hesus.ruiz@gmail.com",
			},
		},
		Usage:     "process a rite document and produce HTML",
		UsageText: "rite [options] [INPUT_FILE] (default input file is index.txt)",
		Action:    processCommandLineAndExecute,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "index",
				Aliases: []string{"i"},
				Value:   defaultIndexFileName,
				Usage:   "the name of the index file in a directory to process (may include other files)",
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "write html to `FILE` (default is input file name with extension .html)",
			},
			&cli.BoolFlag{
				Name:    "norespec",
				Aliases: []string{"p"},
				Usage:   "do not generate using respec semantics, just a plain HTML file",
			},
			&cli.BoolFlag{
				Name:    "dryrun",
				Aliases: []string{"n"},
				Usage:   "do not generate output file, just process input file",
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "run in debug mode",
			},
			&cli.BoolFlag{
				Name:    "watch",
				Aliases: []string{"w"},
				Usage:   "watch the file for changes",
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Println("Error:", err)
	}

}

// processCommandLineAndExecute is the main entry point of the program
func processCommandLineAndExecute(c *cli.Context) error {

	// Default input file name
	var inputFileName = "index.rite"

	// Output file name command line parameter
	outputFileName := c.String("output")

	// The index file to process when working in directory mode
	indexFileName := c.String("index")

	// Dry run
	dryrun := c.Bool("dryrun")

	debugflag = c.Bool("debug")

	// For plain HTML (maybe to integrate in document build chains)
	norespec = c.Bool("norespec")

	// Get the input file name
	if c.Args().Present() {
		inputFileName = c.Args().First()
	} else {
		fmt.Printf("no input file provided, using \"%v\"\n", inputFileName)
	}

	// Get the absolute input path
	absInputPath, err := filepath.Abs(inputFileName)
	if err != nil {
		return err
	}

	// Check if input path is a directory
	finfo, err := os.Stat(absInputPath)
	if err != nil {
		return err
	}

	isDir := finfo.IsDir()

	if isDir {
		fmt.Println("processing directory", absInputPath)
		return processDirectory(absInputPath, indexFileName)
	}

	// Generate the output file name, changing the extension or adding it
	if len(outputFileName) == 0 {
		ext := path.Ext(inputFileName)
		if (len(ext) == 0) || (ext != ".rite") {
			outputFileName = inputFileName + ".html"
		} else {
			outputFileName = strings.Replace(inputFileName, ext, ".html", 1)
		}
	}

	// Print a message
	if !dryrun {
		fmt.Printf("processing %v and generating %v\n", inputFileName, outputFileName)
	} else {
		fmt.Printf("dry run: processing %v without writing output\n", inputFileName)
	}

	// This is useful for development.
	// If the user specified watch, loop forever processing the input file when modified
	if c.Bool("watch") {
		err := processWatch(inputFileName, outputFileName)
		return err
	}

	html := NewParseAndRender(absInputPath)

	// Do nothing if flag dryrun was specified
	if dryrun {
		return nil
	}

	// Write the HTML to the output file
	err = os.WriteFile(outputFileName, []byte(html), 0664)
	if err != nil {
		return err
	}

	return nil
}

func processDirectory(absInputPath string, indexFileName string) error {

	return filepath.WalkDir(absInputPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
			// fmt.Println("Directory", path)
		} else {
			dirName, fileName := filepath.Split(path)
			if fileName == indexFileName {
				var outputFileName string

				fmt.Println("File", path)
				// Generate the output file name, changing the extension or adding it
				ext := filepath.Ext(fileName)
				if (len(ext) == 0) || (ext != ".rite") {
					outputFileName = path + ".html"
				} else {
					outputFileName = strings.Replace(path, ext, ".html", 1)
				}

				html := NewParseAndRender(filepath.Join(dirName, fileName))

				// Write the HTML to the output file
				err = os.WriteFile(outputFileName, []byte(html), 0664)
				if err != nil {
					return err
				}

			}
		}
		return nil

	})
}

// processWatch checks periodically if an input file (inputFileName) has been modified, and if so
// it processes the file and writes the result to the output file (outputFileName)
func processWatch(inputFileName string, outputFileName string) error {

	var old_timestamp time.Time
	var current_timestamp time.Time

	// Loop forever
	for {

		// Get the modified timestamp of the input file
		info, err := os.Stat(inputFileName)
		if err != nil {
			return err
		}
		current_timestamp = info.ModTime()

		// If current modified timestamp is newer than the previous timestamp, process the file
		if old_timestamp.Before(current_timestamp) {

			// Update timestamp for the next cycle
			old_timestamp = current_timestamp

			fmt.Println("************Processing*************")

			// Parse and render the document
			html := NewParseAndRender(inputFileName)

			// And write the new version of the HTML
			err = os.WriteFile(outputFileName, []byte(html), 0664)
			if err != nil {
				return err
			}
		}

		// Check again in one second
		time.Sleep(1 * time.Second)

	}
}

//go:embed assets
var assets embed.FS

func NewParseAndRender(fileName string) string {

	// Open the file and parse it
	p, err := rite.ParseFromFile(fileName, true)
	if err != nil {
		fmt.Printf("error processing %s: %s\n", fileName, err.Error())
		os.Exit(1)
	}

	// Generate the HTML by visiting all the nodes in the parse tree
	fragmentHTML := p.RenderHTML()

	// Initialise the template system. Use the templates specified in the document header,
	// or the default if not specified (assets/templates/respec or assets/templates/standard)
	defaultTemplate := "assets/templates/respec"
	if p.Config.Bool("rite.noReSpec") {
		defaultTemplate = "assets/templates/standard"
	}
	templateDir := p.Config.String("template", defaultTemplate)

	// First check if the user has a local template, otherwise use the embedded one
	var t *template.Template
	_, err = os.Stat(templateDir)
	if err != nil {
		fmt.Println("Using embedded template dir:", templateDir)
		// Parse the embedded templates. Any error stops processing.
		t = template.Must(template.ParseFS(assets, templateDir+"/layouts/*"))
		t = template.Must(t.ParseFS(assets, templateDir+"/partials/*"))
		t = template.Must(t.ParseFS(assets, templateDir+"/pages/*"))
	} else {
		fmt.Println("Using local template dir:", templateDir)
		// Parse all templates in the following directories. Any error stops processing.
		t = template.Must(template.ParseGlob(templateDir + "/layouts/*"))
		t = template.Must(t.ParseGlob(templateDir + "/partials/*"))
		t = template.Must(t.ParseGlob(templateDir + "/pages/*"))
	}

	// Get the bibliography for the references, in the tag "localBiblio"
	// It can be specified in the YAML header or in a separate file in the "localBiblioFile" tag.
	// If both "localBiblio" and "localBiblioFile" exists in the header, only "localBiblio" is used.
	bibData := p.Config.Map("localBiblio", nil)
	if bibData == nil {

		// Read the bibliography file if it exists
		// First try reading the file specified in the YAML header, otherwise use the default name
		bd, err := yaml.ParseYamlFile(p.Config.String("localBiblioFile", "localbiblio.yaml"))
		if err == nil {
			bibData = bd.Map("")
		}
	}

	// Set the data that will be available for the templates
	var data = map[string]any{
		"Config": p.Config.Data(),
		"Biblio": bibData,
		"HTML":   string(fragmentHTML),
	}

	// Execute the template and store the result in memory
	var out bytes.Buffer
	if err := t.ExecuteTemplate(&out, "index.html.tpl", data); err != nil {
		panic(err)
	}

	// Get the raw HTML where we still have to perform some processing
	rawHtml := out.Bytes()

	// Prepare the buffer for efficient editing operations minimizing allocations
	edBuf := sliceedit.NewBuffer(rawHtml)

	// For all IDs that were detected, store the intented changes
	for idName, idNumber := range p.Ids {
		searchString := "{#" + idName + ".num}"
		newValue := fmt.Sprint(idNumber)
		edBuf.ReplaceAllString(searchString, newValue)
	}

	// Replace the HTML escaped codes
	edBuf.ReplaceAllString("\\<", "&lt")
	edBuf.ReplaceAllString("\\>", "&gt")

	// Apply the changes to the buffer and get the HTML
	html := edBuf.String()

	return html
}
