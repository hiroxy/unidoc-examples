/*
 * Detect the number of pages, the color pages (1-offset) and the largest page size in a PDF file.
 *
 * Run as: ./pdf_page_summaries [-d] <file>
 *
 * The results are written to a JSON dict on stdout.
 *
 *  See the other command line options in the top of main()
 *      -d Write debug level logs to stdout
 */

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"

	common "github.com/unidoc/unidoc/common"
	pdfcontent "github.com/unidoc/unidoc/pdf/contentstream"
	pdfcore "github.com/unidoc/unidoc/pdf/core"
	pdf "github.com/unidoc/unidoc/pdf/model"
)

const usage = `Usage:
pdf_page_summaries <file>
-d: Debug level logging
`

var globalDebug = true

func initUniDoc(debug bool) {
	logLevel := common.LogLevelInfo
	if debug {
		logLevel = common.LogLevelDebug
	}
	common.SetLogger(common.ConsoleLogger{LogLevel: logLevel})
}

func main() {
	debug := false // Write debug level info to stdout?

	flag.BoolVar(&debug, "d", false, "Enable debug logging")
	flag.BoolVar(&globalDebug, "e", false, "Enable detailed debug logging")
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	initUniDoc(debug)

	inputPath := args[0]

	if !regularFile(inputPath) {
		common.Log.Error("Not a regular file. %#q", inputPath)
		os.Exit(1)
	}

	numPages, markedPages, width, height, err := describePdfPages(inputPath)
	if err != nil {
		common.Log.Error("describePdfPages failed. err=%v", err)
		os.Exit(1)
	}
	if width <= 1.0 || height <= 1.0 {
		common.Log.Error("Width, Height not specified")
		os.Exit(1)
	}

	err = writeSummary(Summary{
		NumPages:    numPages,
		Width:       width,
		Height:      height,
		MarkedPages: markedPages,
	})
	if err != nil {
		common.Log.Error("describePdfPages failed. err=%v", err)
		os.Exit(1)
	}
}

// describePdfPages reads PDF `inputPath` and returns number of pages, slice of marked page numbers (1-offset)
func describePdfPages(inputPath string) (int, []int, float64, float64, error) {

	f, err := os.Open(inputPath)
	if err != nil {
		return 0, []int{}, 0.0, 0.0, err
	}
	defer f.Close()

	pdfReader, err := pdf.NewPdfReader(f)
	if err != nil {
		return 0, []int{}, 0.0, 0.0, err
	}

	isEncrypted, err := pdfReader.IsEncrypted()
	if err != nil {
		return 0, []int{}, 0.0, 0.0, err
	}
	if isEncrypted {
		_, err = pdfReader.Decrypt([]byte(""))
		if err != nil {
			return 0, []int{}, 0.0, 0.0, err
		}
	}

	numPages, err := pdfReader.GetNumPages()
	if err != nil {
		return numPages, []int{}, 0.0, 0.0, err
	}

	markedPages := []int{}
	var width, height float64

	for i := 0; i < numPages; i++ {
		pageNum := i + 1
		page := pdfReader.PageList[i]
		common.Log.Debug("==========================================================")
		common.Log.Debug("page %d", pageNum)

		w, h, _ := pageSize(page)
		if w > width {
			width = w
		}
		if h > height {
			height = h
		}

		desc := fmt.Sprintf("%s:page%d", filepath.Base(inputPath), pageNum)
		marked, err := isPageMarked(page, desc, globalDebug)
		if err != nil {
			return numPages, markedPages, width, height, err
		}
		if marked {
			markedPages = append(markedPages, pageNum)
		}
	}

	return numPages, markedPages, width, height, nil
}

// =================================================================================================
// Page object detection code goes here
// =================================================================================================

// isPageMarked returns true if `page` contains color. It also references
// XObject Images and Forms to _possibly_ record if they contain color
func isPageMarked(page *pdf.PdfPage, desc string, debug bool) (bool, error) {
	// For each page, we go through the resources and look for the images.

	contents, err := page.GetAllContentStreams()
	if err != nil {
		common.Log.Error("GetAllContentStreams failed. err=%v", err)
		return false, err
	}

	if debug {
		fmt.Println("\n===============***================")
		fmt.Printf("%s\n", desc)
		fmt.Println("===============+++================")
		fmt.Printf("%s\n", contents)
		fmt.Println("==================================")
	}

	marked, err := isContentStreamMarked(contents, page.Resources, debug)
	common.Log.Debug("marked=%t err=%v", marked, err)

	if err != nil {
		common.Log.Error("isContentStreamMarked failed. err=%v", err)
		return false, err
	}
	return marked, nil
}

// isContentStreamMarked returns true if `contents` contains any marking object
func isContentStreamMarked(contents string, resources *pdf.PdfPageResources, debug bool) (bool, error) {
	cstreamParser := pdfcontent.NewContentStreamParser(contents)
	operations, err := cstreamParser.Parse()
	if err != nil {
		return false, err
	}

	marked := false                                     // Has a mark been detected in the stream?
	markingPatterns := map[pdfcore.PdfObjectName]bool{} // List of already detected patterns. Re-use for subsequent detections.

	// The content stream processor keeps track of the graphics state and we can make our own handlers to process
	// certain commands using the AddHandler method. In this case, we hook up to color related operands, and for image
	// and form handling.
	processor := pdfcontent.NewContentStreamProcessor(*operations)
	// Add handlers for colorspace related functionality.
	processor.AddHandler(pdfcontent.HandlerConditionEnumAllOperands, "",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState,
			resources *pdf.PdfPageResources) error {
			if marked {
				return nil
			}
			operand := op.Operand
			switch operand {
			case "SC", "SCN": // Set stroking color.  Includes pattern colors.
				if isPatternCS(gs.ColorspaceStroking) {
					op := pdfcontent.ContentStreamOperation{}
					op.Operand = operand
					op.Params = []pdfcore.PdfObject{}

					patternColor, ok := gs.ColorStroking.(*pdf.PdfColorPattern)
					if !ok {
						return errors.New("Invalid stroking color type")
					}

					if patternColor.Color != nil {
						if isColorMarking(patternColor.Color) {
							common.Log.Debug("op=%s hasMarking=%t", op, true)
							marked = true
							return nil
						}
					}

					if hasMarking, ok := markingPatterns[patternColor.PatternName]; ok {
						// Already processed, need not change anything, except underlying color if used.
						if hasMarking {
							common.Log.Debug("op=%s hasMarking=%t", op, hasMarking)
							marked = true
						}
						return nil
					}

					// Look up the pattern name and convert it.
					pattern, found := resources.GetPatternByName(patternColor.PatternName)
					if !found {
						return errors.New("Undefined pattern name")
					}
					hasMarking, err := isPatternMarked(pattern, debug)
					if err != nil {
						common.Log.Error("isPatternMarked failed. err=%v", err)
						return err
					}
					markingPatterns[patternColor.PatternName] = hasMarking
					marked = marked || hasMarking
					common.Log.Debug("op=%s hasMarking=%t", op, hasMarking)

				} else {
					hasMarking := isColorMarking(gs.ColorStroking)
					marked = marked || hasMarking
					common.Log.Debug("op=%s ColorspaceStroking=%T ColorStroking=%#v hasMarking=%t",
						op, gs.ColorspaceStroking, gs.ColorStroking, hasMarking)
				}
				return nil
			case "sc", "scn": // Set non-stroking color.
				if isPatternCS(gs.ColorspaceNonStroking) {
					op := pdfcontent.ContentStreamOperation{}
					op.Operand = operand
					op.Params = []pdfcore.PdfObject{}
					patternColor, ok := gs.ColorNonStroking.(*pdf.PdfColorPattern)
					if !ok {
						return errors.New("Invalid stroking color type")
					}
					if patternColor.Color != nil {
						hasMarking := isColorMarking(patternColor.Color)
						marked = marked || hasMarking
						common.Log.Debug("op=%#v hasMarking=%t", op, hasMarking)
					}
					if hasMarking, ok := markingPatterns[patternColor.PatternName]; ok {
						// Already processed, need not change anything, except underlying color if used.
						marked = marked || hasMarking
						common.Log.Debug("op=%#v hasMarking=%t", op, hasMarking)
						return nil
					}

					// Look up the pattern name and convert it.
					pattern, found := resources.GetPatternByName(patternColor.PatternName)
					if !found {
						return errors.New("Undefined pattern name")
					}
					hasMarking, err := isPatternMarked(pattern, debug)
					if err != nil {
						common.Log.Debug("Unable to convert pattern to grayscale: %v", err)
						return err
					}
					markingPatterns[patternColor.PatternName] = hasMarking
				} else {
					hasMarking := isColorMarking(gs.ColorNonStroking)
					marked = marked || hasMarking
					common.Log.Debug("op=%s ColorspaceNonStroking=%T ColorNonStroking=%#v hasMarking=%t",
						op, gs.ColorspaceNonStroking, gs.ColorNonStroking, hasMarking)

				}
				return nil
			case "G", "RG", "K": // Set Gray, RGB or CMYK stroking color.
				hasMarking := isColorMarking(gs.ColorStroking)
				common.Log.Debug("op=%s ColorspaceStroking=%T ColorStroking=%#v hasMarking=%t",
					op, gs.ColorspaceStroking, gs.ColorStroking, hasMarking)
				marked = marked || hasMarking
				return nil
			case "g", "rg", "k": // Set Gray, RGB or CMYK as non-stroking color.
				hasMarking := isColorMarking(gs.ColorNonStroking)
				marked = marked || hasMarking
				common.Log.Debug("op=%s ColorspaceStroking=%T ColorStroking=%#v hasMarking=%t",
					op, gs.ColorspaceStroking, gs.ColorStroking, hasMarking)
				return nil
			case "sh": // Paints the shape and color defined by shading dict.
				if len(op.Params) != 1 {
					return errors.New("Params to sh operator should be 1")
				}
				_, ok := op.Params[0].(*pdfcore.PdfObjectName)
				if !ok {
					return errors.New("sh parameter should be a name")
				}
				marked = true
			}
			return nil
		})

	// Add handler for image related handling.  Note that inline images are completely stored with a
	// ContentStreamInlineImage object as the parameter for BI.
	processor.AddHandler(pdfcontent.HandlerConditionEnumOperand, "BI",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState, resources *pdf.PdfPageResources) error {
			if marked {
				return nil
			}
			if len(op.Params) != 1 {
				err := errors.New("invalid number of parameters")
				common.Log.Error("BI error. err=%v")
				return err
			}
			// Inline image.
			iimg, ok := op.Params[0].(*pdfcontent.ContentStreamInlineImage)
			if !ok {
				common.Log.Error("Invalid handling for inline image")
				return errors.New("Invalid inline image parameter")
			}
			common.Log.Debug("iimg=%s", iimg)

			cs, err := iimg.GetColorSpace(resources)
			if err != nil {
				common.Log.Error("Error getting color space for inline image: %v", err)
				return err
			}

			if cs.GetNumComponents() == 1 {
				return nil
			}

			encoder, err := iimg.GetEncoder()
			if err != nil {
				common.Log.Error("Error getting encoder for inline image: %v", err)
				return err
			}

			switch encoder.GetFilterName() {
			// TODO: Add JPEG2000 encoding/decoding. Until then we assume JPEG200 images are color
			case "JPXDecode":
				return nil
			// These filters are only used with grayscale images
			case "CCITTDecode", "JBIG2Decode":
				return nil
			}

			img, err := iimg.ToImage(resources)
			if err != nil {
				common.Log.Error("Error converting inline image to image: %v", err)
				return err
			}

			common.Log.Debug("img=%v %d", img.ColorComponents, img.BitsPerComponent)

			rgbImg, err := cs.ImageToRGB(*img)
			if err != nil {
				common.Log.Error("Error converting image to rgb: %v", err)
				return err
			}
			hasMarking := isRgbImageColored(rgbImg, debug)
			marked = marked || hasMarking
			common.Log.Debug("hasMarking=%t", hasMarking)

			return nil
		})

	// Handler for XObject Image and Forms.
	processedXObjects := map[string]bool{} // Keep track of processed XObjects to avoid repetition.

	processor.AddHandler(pdfcontent.HandlerConditionEnumOperand, "Do",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState, resources *pdf.PdfPageResources) error {
			if marked {
				return nil
			}

			if len(op.Params) < 1 {
				common.Log.Error("Invalid number of params for Do object")
				return errors.New("Range check")
			}

			// XObject.
			name := op.Params[0].(*pdfcore.PdfObjectName)
			common.Log.Debug("Name=%#v=%#q", name, string(*name))

			// Only process each one once.
			hasMarking, has := processedXObjects[string(*name)]
			common.Log.Debug("name=%q has=%t hasMarking=%t processedXObjects=%+v",
				*name, has, hasMarking, processedXObjects)
			if has {
				marked = marked || hasMarking
				return nil
			}
			processedXObjects[string(*name)] = false

			_, xtype := resources.GetXObjectByName(*name)
			common.Log.Debug("xtype=%+v pdf.XObjectTypeImage=%v", xtype, pdf.XObjectTypeImage)

			if xtype == pdf.XObjectTypeImage {
				ximg, err := resources.GetXObjectImageByName(*name)
				if err != nil {
					common.Log.Error("Error w/GetXObjectImageByName : %v", err)
					return err
				}
				common.Log.Debug("!!Filter=%s ColorSpace=%s ImageMask=%v wxd=%dx%d",
					ximg.Filter.GetFilterName(), ximg.ColorSpace,
					ximg.ImageMask, *ximg.Width, *ximg.Height)
				// Ignore gray color spaces
				if _, isIndexed := ximg.ColorSpace.(*pdf.PdfColorspaceSpecialIndexed); !isIndexed {
					if ximg.ColorSpace.GetNumComponents() == 1 {
						return nil
					}
				}
				switch ximg.Filter.GetFilterName() {
				// TODO: Add JPEG2000 encoding/decoding. Until then we assume JPEG200 images are color
				case "JPXDecode":
					processedXObjects[string(*name)] = true
					marked = true
					return nil
				// These filters are only used with grayscale images
				case "CCITTDecode", "JBIG2Decode":

					return nil
				}

				// Hacky workaround for Szegedy_Going_Deeper_With_2015_CVPR_paper.pdf that has a marked image
				// that is completely masked
				if ximg.Filter.GetFilterName() == "RunLengthDecode" && ximg.SMask != nil {
					return nil
				}

				img, err := ximg.ToImage()
				if err != nil {
					common.Log.Error("Error w/ToImage: %v", err)
					return err
				}

				rgbImg, err := ximg.ColorSpace.ImageToRGB(*img)
				if err != nil {
					common.Log.Error("Error ImageToRGB: %v", err)
					return err
				}

				common.Log.Debug("img: ColorComponents=%d wxh=%dx%d", img.ColorComponents, img.Width, img.Height)
				common.Log.Debug("ximg: ColorSpace=%T=%s mask=%v", ximg.ColorSpace, ximg.ColorSpace, ximg.Mask)
				common.Log.Debug("rgbImg: ColorComponents=%d wxh=%dx%d", rgbImg.ColorComponents, rgbImg.Width, rgbImg.Height)

				hasMarking := isRgbImageColored(rgbImg, debug)
				processedXObjects[string(*name)] = hasMarking
				marked = marked || hasMarking
				common.Log.Debug("hasMarking=%t", hasMarking)

			} else if xtype == pdf.XObjectTypeForm {
				common.Log.Debug(" XObject Form: %s", *name)

				// Go through the XObject Form content stream.
				xform, err := resources.GetXObjectFormByName(*name)
				if err != nil {
					common.Log.Error("err=%v", err)
					return err
				}

				formContent, err := xform.GetContentStream()
				if err != nil {
					common.Log.Error("err=%v")
					return err
				}

				// Process the content stream in the Form object too:
				// XXX/TODO/Consider: Use either form resources (priority) and fall back to page resources alternatively
				// if not found.
				// Have not come into cases where needed yet.
				formResources := xform.Resources
				if formResources == nil {
					formResources = resources
				}

				// Process the content stream in the Form object too:
				hasMarking, err := isContentStreamMarked(string(formContent), formResources, debug)
				if err != nil {
					common.Log.Error("err=%v", err)
					return err
				}
				processedXObjects[string(*name)] = hasMarking
				marked = marked || hasMarking
				common.Log.Debug("hasMarking=%t", hasMarking)

			}

			return nil
		})

	err = processor.Process(resources)
	if err != nil {
		common.Log.Error("processor.Process returned: err=%v", err)
		return false, err
	}

	return marked, nil
}

// isPatternCS returns true if `colorspace` represents a Pattern colorspace.
func isPatternCS(cs pdf.PdfColorspace) bool {
	_, isPattern := cs.(*pdf.PdfColorspaceSpecialPattern)
	return isPattern
}

// isPatternMarked returns true if `pattern` contains color (tiling or shading pattern).
func isPatternMarked(pattern *pdf.PdfPattern, debug bool) (bool, error) {
	// Case 1: Colored tiling patterns.  Need to process the content stream and replace.
	if pattern.IsTiling() {
		tilingPattern := pattern.GetAsTilingPattern()

		// A marked tiling pattern can use color operators in its stream, need to process the stream.
		content, _, err := tilingPattern.GetContentStream()
		if err != nil {
			return false, err
		}
		marked, err := isContentStreamMarked(string(content), tilingPattern.Resources, debug)
		return marked, err

	} else if pattern.IsShading() {
		// Case 2: Shading patterns.  Need to create a new colorspace that can map from N=3,4 colorspaces to grayscale.
		return true, nil
	}
	common.Log.Error("isPatternMarked. pattern is neither tiling nor shading")
	return false, nil
}

func isColorMarking(color pdf.PdfColor) bool {
	marking := isColorMarking_(color)
	common.Log.Debug("isColorMarking: %T %t", color, marking)
	// if marking {
	// 	panic("RRRR")
	// }
	return marking
}

// isColorMarking returns true if `color` is visibleAdditive
func isColorMarking_(color pdf.PdfColor) bool {
	switch color.(type) {
	case *pdf.PdfColorDeviceGray:
		col := color.(*pdf.PdfColorDeviceGray)
		return visibleAdditive(col.Val())
	case *pdf.PdfColorDeviceRGB:
		col := color.(*pdf.PdfColorDeviceRGB)
		return visibleAdditive(col.R(), col.G(), col.B())
	case *pdf.PdfColorDeviceCMYK:
		col := color.(*pdf.PdfColorDeviceCMYK)
		return visibleSubtractive(col.C(), col.M(), col.Y(), col.K())
	case *pdf.PdfColorCalGray:
		col := color.(*pdf.PdfColorCalGray)
		return visibleAdditive(col.Val())
	case *pdf.PdfColorCalRGB:
		col := color.(*pdf.PdfColorCalRGB)
		return visibleAdditive(col.A(), col.B(), col.C())
	case *pdf.PdfColorLab:
		col := color.(*pdf.PdfColorLab)
		return visibleAdditive(col.L())
	}
	common.Log.Error("isColorMarking: Unknown color %T %s", color, color)
	panic("Unknown color type")
}

// isRgbImageColored returns true if `img` contains any color pixels
func isRgbImageColored(img pdf.Image, debug bool) bool {

	samples := img.GetSamples()
	maxVal := math.Pow(2, float64(img.BitsPerComponent)) - 1

	for i := 0; i < len(samples); i += 3 {
		// Normalized data, range 0-1.
		r := float64(samples[i]) / maxVal
		g := float64(samples[i+1]) / maxVal
		b := float64(samples[i+2]) / maxVal
		if visibleAdditive(r, g, b) {
			common.Log.Debug("@@ marked pixel: i=%d rgb=%.3f %.3f %.3f", i, r, g, b)
			common.Log.Debug("                 delta rgb=%.3f %.3f %.3f", r-g, r-b, g-b)
			common.Log.Debug("            additiveZero=%.3f", additiveZero)
			return true
		}
	}
	return false
}

// ColorTolerance is the smallest color component that is visible on a typical mid-range color laser printer
// cpts have values in range 0.0-1.0
const subtractiveZero = 3.1 / 255.0
const additiveZero = 1.0 - subtractiveZero

// visibleAdditive returns true if any of color component `cpts` is visible on a typical mid-range color laser printer
// cpts have values in range 0.0-1.0
func visibleAdditive(cpts ...float64) bool {
	for i, x := range cpts {
		if math.Abs(x) < additiveZero {
			common.Log.Debug("visibleAdditive: i=%d x=%.3f", i, x)
			return true
		}
	}
	return false
}

func visibleSubtractive(cpts ...float64) bool {
	for i, x := range cpts {
		if math.Abs(x) > subtractiveZero {
			common.Log.Debug("visibleSubtractive: i=%d x=%.3f", i, x)
			return true
		}
	}
	return false
}

// pageSize returns the width and height of `page` in mm
func pageSize(page *pdf.PdfPage) (float64, float64, error) {
	mediaBox, err := page.GetMediaBox()
	if err != nil {
		return 0.0, 0.0, nil
	}
	return toMM(mediaBox.Urx - mediaBox.Llx), toMM(mediaBox.Ury - mediaBox.Lly), nil
}

// toMM takes the absolute value of `x` in points, converts to mm and rounds to nearest .1 mm
func toMM(x float64) float64 {
	y := math.Abs(x) / 72.0 * 25.4
	return math.Floor(y*10+0.5) / 10.0
}

// regularFile returns true if file `path` is a regular file
func regularFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	return fi.Mode().IsRegular()
}

type Summary struct {
	NumPages    int
	Width       float64
	Height      float64
	MarkedPages []int
}

func writeSummary(a Summary) error {
	b, err := json.Marshal(a)
	if err != nil {
		common.Log.Error("writeSummary: err=%v", err)
		return err
	}
	fmt.Printf("%s\n", b)
	return nil
}
