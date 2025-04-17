// Copyright 2023-2024 Jesus Ruiz. All rights reserved.
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

const (
	defaultIndexFileName    = "index.rite"
	htmlExtension           = ".html"
	indexTemplateName       = "index.html.tpl"
	defaultRespecTemplate   = "assets/templates/respec"
	defaultStandardTemplate = "assets/templates/standard"
)

func main() {

	version := "v0.10.7"

	// Get the version control info, to embed in the program version
	buildInfo, ok := debug.ReadBuildInfo()
	if ok {
		buildSettings := buildInfo.Settings
		for _, setting := range buildSettings {
			if setting.Key == "vcs.time" {
				version = version + ", built on " + setting.Value
			}
			if setting.Key == "vcs.revision" {
				version = version + ", revision " + setting.Value
			}
		}

	}

	// Define the commands of the CLI
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
		UsageText: "rite [options] [INPUT_FILE] (default input file is index.rite)",
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

	// Output file name command line parameter
	outputFileName := c.String("output")

	// The index file to process when working in directory mode
	indexFileName := c.String("index")

	// Dry run
	dryrun := c.Bool("dryrun")

	debugflag = c.Bool("debug")

	// For plain HTML (for example, to integrate in document build chains)
	norespec = c.Bool("norespec")

	// Get the input file name
	var inputFileName = defaultIndexFileName
	if c.Args().Present() {
		inputFileName = c.Args().First()
	} else {
		fmt.Printf("no input file provided, using \"%v\"\n", inputFileName)
	}

	// Get the absolute input path
	absInputPath, err := filepath.Abs(inputFileName)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", inputFileName, err)
	}

	// Check if input path is a directory
	finfo, err := os.Stat(absInputPath)
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", absInputPath, err)
	}

	isDir := finfo.IsDir()

	// If the user specified a directory, process it and return
	if isDir {
		fmt.Println("processing directory", absInputPath)
		return processDirectory(absInputPath, indexFileName)
	}

	// At this moment, we know that the user specified a file

	// Generate the output file name, changing the extension or adding it
	if len(outputFileName) == 0 {
		outputFileName = strings.TrimSuffix(inputFileName, path.Ext(inputFileName)) + htmlExtension
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
		return fmt.Errorf("running processWatch with %s and %s: %w", inputFileName, outputFileName, err)
	}

	html, err := NewParseAndRender(absInputPath)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", absInputPath, err)
	}

	// Do nothing if flag dryrun was specified
	if dryrun {
		return nil
	}

	// Write the HTML to the output file
	err = os.WriteFile(outputFileName, []byte(html), 0664)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", outputFileName, err)
	}

	return nil
}

// processDirectory visits recursively a directory tree, processing each index file found in each directory.
func processDirectory(absInputPath string, indexFileName string) error {

	// Visit recursively all entries (files and directories) in the specified directory and its subdirectories
	// We will process only the files which match exactly the name specified in 'indexFileName'
	return filepath.WalkDir(absInputPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			// Do nothing with a directory, continue visiting other entries
			return nil
		}

		// Do nothing if the file does not have the proper name
		dirName, fileName := filepath.Split(path)
		if fileName != indexFileName {
			return nil
		}

		// Generate the output file name, derived from the input file name
		var outputFileName string
		ext := filepath.Ext(fileName)
		if (len(ext) == 0) || (ext != ".rite") {
			outputFileName = path + htmlExtension
		} else {
			outputFileName = strings.Replace(path, ext, htmlExtension, 1)
		}

		// Parse the input file and get the HTML
		html, err := NewParseAndRender(filepath.Join(dirName, fileName))
		if err != nil {
			return fmt.Errorf("parsing %s: %w", filepath.Join(dirName, fileName), err)
		}

		// Write the HTML to the output file
		err = os.WriteFile(outputFileName, []byte(html), 0664)
		if err != nil {
			return fmt.Errorf("failed to write HTML file %s: %w", outputFileName, err)
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
			fmt.Printf("Error getting file info for %s: %v\n", inputFileName, err)
			// Continue the loop instead of returning
			time.Sleep(1 * time.Second)
			continue
		}
		current_timestamp = info.ModTime()

		// If current modified timestamp is newer than the previous timestamp, process the file
		if old_timestamp.Before(current_timestamp) {

			// Update timestamp for the next cycle
			old_timestamp = current_timestamp

			fmt.Println("************Processing*************")

			// Parse and render the document
			html, err := NewParseAndRender(inputFileName)
			if err != nil {
				fmt.Printf("Error parsing file %s: %v\n", inputFileName, err)
				// Continue the loop instead of returning
				time.Sleep(1 * time.Second)
				continue
			}

			// And write the new version of the HTML
			err = os.WriteFile(outputFileName, []byte(html), 0664)
			if err != nil {
				fmt.Printf("Error writing file %s: %v\n", outputFileName, err)
				// Continue the loop instead of returning
				time.Sleep(1 * time.Second)
				continue
			}
		}

		// Check again in one second
		time.Sleep(1 * time.Second)

	}
}

//go:embed assets
var assets embed.FS

// NewParseAndRender processes a file named fileName, and all assets referenced from it
func NewParseAndRender(fileName string) (string, error) {

	// Get the absolute name of the file, in preparation to get the directory and file name
	absoluteFileName, err := filepath.Abs(fileName)
	if err != nil {
		return "", fmt.Errorf("getting absolute file name for %s: %w", fileName, err)
	}

	directory, fileName := filepath.Split(absoluteFileName)

	// Open the file and parse it
	parser, err := rite.ParseFromFile(fileName)
	if err != nil {
		return "", fmt.Errorf("processing %s: %w", fileName, err)
	}

	// Generate the HTML by visiting all the nodes in the parse tree
	fragmentHTML := parser.RenderHTML()

	// Initialise the template system. Use the templates specified in the document header,
	// or the default if not specified (assets/templates/respec or assets/templates/standard)
	templateDir := defaultRespecTemplate

	// If the user has configured not to use the 'respec' template, we use the 'standard' template
	if parser.Config.Bool("rite.noReSpec") || parser.Config.Bool("rite.norespec") {
		templateDir = defaultStandardTemplate
	}

	// But the specific template in the command line overrides all of them
	templateDir = parser.Config.String("template", templateDir)

	// First check if the user has a local template, otherwise use the embedded one
	var t *template.Template
	_, err = os.Stat(templateDir)
	if err != nil {

		// The template directory does NOT exist in the local disk
		// Parse the embedded templates. Any error stops processing.
		fmt.Println("Using embedded template dir:", templateDir)
		t = template.Must(template.ParseFS(assets, templateDir+"/layouts/*"))
		t = template.Must(t.ParseFS(assets, templateDir+"/partials/*"))
		t = template.Must(t.ParseFS(assets, templateDir+"/pages/*"))

	} else {

		// The template directory DOES exist in the local disk
		// Parse all templates in the proper directories. Any error stops processing.
		fmt.Println("Using local template dir:", templateDir)
		t = template.Must(template.ParseGlob(templateDir + "/layouts/*"))
		t = template.Must(t.ParseGlob(templateDir + "/partials/*"))
		t = template.Must(t.ParseGlob(templateDir + "/pages/*"))

	}

	// Get the bibliography for the references, in the tag "localBiblio"
	// It can be specified in the YAML header or in a separate file in the "localBiblioFile" tag.
	// If both "localBiblio" and "localBiblioFile" exists in the header, only "localBiblio" is used.
	bibData := parser.Config.Map("localBiblio", nil)
	if bibData == nil {

		// Bibliography data does NOT exist directly in the file being processed
		// Try to see if the file specifies a SEPARATE file with bibliography data
		// First try reading the file specified in the YAML header, otherwise use the default name
		// The biblio file name is relative to the location of the file we are processing
		relativeBiblioFile := parser.Config.String("localBiblioFile", "localbiblio.yaml")
		absoluteBiblioFile := filepath.Join(directory, relativeBiblioFile)
		bd, err := yaml.ParseYamlFile(absoluteBiblioFile)
		if err == nil {
			bibData = bd.Map("")
		}
	}

	// Set the data that will be available for the templates
	var data = map[string]any{
		"Config": parser.Config.Data(),
		"Biblio": bibData,
		"HTML":   string(fragmentHTML),
	}

	// Execute the template and store the result in memory
	var out bytes.Buffer
	if err := t.ExecuteTemplate(&out, indexTemplateName, data); err != nil {
		return "", fmt.Errorf("processing template %s with file: %s: %w", indexTemplateName, fileName, err)
	}

	// Get the raw HTML where we still have to perform some processing
	rawHtml := out.Bytes()

	// Prepare the buffer for efficient editing operations minimizing allocations
	editBuffer := sliceedit.NewBuffer(rawHtml)

	// For all IDs that were detected, store the intented changes
	for idName, idNumber := range parser.Ids {
		searchString := "{#" + idName + ".num}"
		newValue := fmt.Sprint(idNumber)
		editBuffer.ReplaceAllString(searchString, newValue)
	}

	// Replace the HTML escaped codes
	editBuffer.ReplaceAllString("\\<", "&lt")
	editBuffer.ReplaceAllString("\\>", "&gt")

	// Apply the changes to the buffer and get the HTML
	html := editBuffer.String()

	return html, nil
}
