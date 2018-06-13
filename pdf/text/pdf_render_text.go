/*
 * PDF to text: Extract all text for each page of a pdf file.
 *
 * Run as: go run pdf_extract_text.go input.pdf
 */

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/license"
	"github.com/unidoc/unidoc/common"
	pdfcore "github.com/unidoc/unidoc/pdf/core"
	"github.com/unidoc/unidoc/pdf/extractor"
	pdf "github.com/unidoc/unidoc/pdf/model"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: go run pdf_render_text.go input.pdf\n")
		os.Exit(1)
	}

	// Make sure to enter a valid license key.
	// Otherwise text is truncated and a watermark added to the text.
	// License keys are available via: https://unidoc.io
	/*
			license.SetLicenseKey(`
		-----BEGIN UNIDOC LICENSE KEY-----
		...key contents...
		-----END UNIDOC LICENSE KEY-----
		`)
	*/

	// For debugging.
	common.SetLogger(common.NewConsoleLogger(common.LogLevelDebug))
	files := os.Args[1:]
	sort.Strings(files)

	exclusions := map[string]bool{
		`The-Byzantine-Generals-Problem.pdf`: true,
		`endosymbiotictheory_marguli.pdf`:    true,
		`iverson.pdf`:                        true,
		`p253-porter.pdf`:                    true,
		`shamirturing.pdf`:                   true,
		`warnock_camelot.pdf`:                true,
		`B02.pdf`:                            true,
	}
	files2 := []string{}
	for _, inputPath := range files {
		if _, ok := exclusions[filepath.Base(inputPath)]; ok {
			continue
		}
		if strings.Contains(inputPath, "xxx.hard") {
			continue
		}
		files2 = append(files2, inputPath)
	}
	files = files2

	for i, inputPath := range files {
		fmt.Println("======================== ^^^ ========================")
		fmt.Printf("Pdf File %3d of %d %q\n", i+1, len(files), inputPath)
		err = outputPdfText(inputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Pdf File %3d of %d %q err=%v\n", i+1, len(files), inputPath, err)
			if err == pdf.ErrEncrypted || err == pdfcore.ErrNoPdfVersion {
				continue
			}
			os.Exit(1)
		}
		fmt.Println("======================== ||| ========================")
	}
	fmt.Fprintf(os.Stderr, "Done %d files\n", len(files))
}

// outputPdfText prints out contents of PDF file to stdout.
func outputPdfText(inputPath string) error {
	f, err := os.Open(inputPath)
	if err != nil {
		return err
	}

	defer f.Close()

	pdfReader, err := pdf.NewPdfReader(f)
	if err != nil {
		return err
	}

	numPages, err := pdfReader.GetNumPages()
	if err != nil {
		return err
	}

	fmt.Println("---------------------------------------")
	fmt.Printf("PDF text rendering: %q\n", inputPath)
	fmt.Println("---------------------------------------")
	for i := 0; i < numPages; i++ {
		pageNum := i + 1

		page, err := pdfReader.GetPage(pageNum)
		if err != nil {
			return err
		}

		ex, err := extractor.New(page)
		if err != nil {
			return err
		}

		text, err := ex.ExtractText()
		if err != nil {
			return err
		}

		// fmt.Println("---------------------------------------")
		fmt.Printf("Page %d:\n", pageNum)
		fmt.Printf("%#q\n", text)
		fmt.Println("---------------------------------------")
	}

	return nil
}
