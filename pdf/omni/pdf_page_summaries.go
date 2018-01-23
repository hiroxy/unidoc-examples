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
	"strings"

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

type OpMark struct {
	marking  bool
	stroking bool
	filling  bool
}

// markingOperators[op] is true if operator `op` marks the page
var markingOperators = map[string]OpMark{
	`b`:   {true, true, true},    //   0: closepath, fill, stroke Close, fill, and stroke path using nonzero winding number rule
	`B`:   {true, true, true},    //   1: fill, stroke Fill and stroke path using nonzero winding number rule
	`b*`:  {true, true, true},    //   2: closepath, eofill, stroke Close, fill, and stroke path using even-odd rule
	`B*`:  {true, true, true},    //   3: eofill, stroke Fill and stroke path using even-odd rule
	`BDC`: {false, false, false}, //  !@#$ 4: (PDF 1.2) Begin marked-content sequence with property list
	`BI`:  {true, false, false},  //   5: Begin inline image object
	`BMC`: {false, false, false}, //   !@#$ 6: (PDF 1.2) Begin marked-content sequence
	`BT`:  {false, false, false}, //   !@#$ 7: Begin text object
	`BX`:  {false, false, false}, //   !@#$ 8: (PDF 1.1) Begin compatibility section
	`c`:   {false, false, false}, //   9: curveto Append curved segment to path (three control points)
	`cm`:  {false, false, false}, //  10: concat Concatenate matrix to current transformation matrix
	`CS`:  {false, false, false}, //  11: setcolorspace (PDF 1.1) Set color space for stroking operations
	`cs`:  {false, false, false}, //  12: setcolorspace (PDF 1.1) Set color space for nonstroking operations
	`d`:   {false, false, false}, //  13: setdash Set line dash pattern
	`d0`:  {false, false, false}, //  14: setcharwidth Set glyph width in Type 3 font
	`d1`:  {false, false, false}, //  15: setcachedevice Set glyph width and bounding box in Type 3 font
	`Do`:  {false, false, false}, //  16: Invoke named XObject
	`DP`:  {false, false, false}, //  17: (PDF 1.2) Define marked-content point with property list
	`EI`:  {true, false, false},  //  18: End inline image object
	`EMC`: {false, false, false}, //  !@#$ 19: (PDF 1.2) End marked-content sequence
	`ET`:  {false, false, false}, //  !@#$ 20: End text object
	`EX`:  {false, false, false}, //  !@#$ 21: (PDF 1.1) End compatibility section
	`f`:   {true, false, true},   //  22: fill Fill path using nonzero winding number rule
	`F`:   {true, false, true},   //  23: fill Fill path using nonzero winding number rule (obsolete)
	`f*`:  {true, false, true},   //  24: eofill Fill path using even-odd rule
	`G`:   {false, false, false}, //  25: setgray Set gray level for stroking operations
	`g`:   {false, false, false}, //  26: setgray Set gray level for nonstroking operations
	`gs`:  {false, false, false}, //  27: (PDF 1.2) Set parameters from graphics state parameter dictionary
	`h`:   {false, false, false}, //  28: closepath Close subpath
	`i`:   {false, false, false}, //  29: setflat Set flatness tolerance
	`ID`:  {true, false, false},  //  30: Begin inline image data
	`j`:   {false, false, false}, //  31: setlinejoin Set line join style
	`J`:   {false, false, false}, //  32: setlinecap Set line cap style
	`K`:   {false, false, false}, //  33: setcmykcolor Set CMYK color for stroking operations
	`k`:   {false, false, false}, //  34: setcmykcolor Set CMYK color for nonstroking operations
	`l`:   {false, false, false}, //  35: lineto Append straight line segment to path
	`m`:   {false, false, false}, //  36: moveto Begin new subpath
	`M`:   {false, false, false}, //  37: setmiterlimit Set miter limit
	`MP`:  {false, false, false}, //  38: (PDF 1.2) Define marked-content point
	`n`:   {false, false, false}, //  39: End path without filling or stroking
	`q`:   {false, false, false}, //  40: gsave Save graphics state
	`Q`:   {false, false, false}, //  41: grestore Restore graphics state
	`re`:  {false, false, false}, //  42: Append rectangle to path
	`RG`:  {false, false, false}, //  43: setrgbcolor Set RGB color for stroking operations
	`rg`:  {false, false, false}, //  44: setrgbcolor Set RGB color for nonstroking operations
	`ri`:  {false, false, false}, //  45: Set color rendering intent
	`s`:   {true, true, false},   //  46: closepath, stroke Close and stroke path
	`S`:   {true, true, false},   //  47: stroke Stroke path
	`SC`:  {false, false, false}, //  48: setcolor (PDF 1.1) Set color for stroking operations
	`sc`:  {false, false, false}, //  49: setcolor (PDF 1.1) Set color for nonstroking operations
	`SCN`: {false, false, false}, //  50: setcolor (PDF 1.2) Set color for stroking operations (ICCBased and special colour spaces)
	`scn`: {false, false, false}, //  51: setcolor (PDF 1.2) Set color for nonstroking operations (ICCBased and special colour spaces)
	`sh`:  {true, true, true},    //  52: shfill (PDF 1.3) Paint area defined by shading pattern
	`T*`:  {false, false, false}, //  53: Move to start of next text line
	`Tc`:  {false, false, false}, //  54: Set character spacing
	`Td`:  {false, false, false}, //  55: Move text position
	`TD`:  {false, false, false}, //  56: Move text position and set leading
	`Tf`:  {false, false, false}, //  57: selectfont Set text font and size
	`Tj`:  {true, true, true},    //  58: show Show text
	`TJ`:  {true, true, true},    //  59: Show text, allowing individual glyph positioning
	`TL`:  {false, false, false}, //  60: Set text leading
	`Tm`:  {false, false, false}, //  61: Set text matrix and text line matrix
	`Tr`:  {false, false, false}, //  62: Set text rendering mode
	`Ts`:  {false, false, false}, //  63: Set text rise
	`Tw`:  {false, false, false}, //  64: Set word spacing
	`Tz`:  {false, false, false}, //  65: Set horizontal text scaling
	`v`:   {false, false, false}, //  66: curveto Append curved segment to path (initial point replicated)
	`w`:   {false, false, false}, //  67: setlinewidth Set line width
	`W`:   {false, false, false}, //  68: clip Set clipping path using nonzero winding number rule
	`W*`:  {false, false, false}, //  69: eoclip Set clipping path using even-odd rule
	`y`:   {false, false, false}, //  70: curveto Append curved segment to path (final point replicated)
	`'`:   {true, true, true},    //  71: Move to next line and show text
	"\"":  {true, true, true},    //  72: Set word and character spacing, move to next line, and show text
}

// isContentStreamMarked returns true if `contents` contains any marking object
func isContentStreamMarked(contents string, resources *pdf.PdfPageResources, debug bool) (bool, error) {
	cstreamParser := pdfcontent.NewContentStreamParser(contents)
	operations, err := cstreamParser.Parse()
	if err != nil {
		return false, err
	}

	visibleStroke := true                               // Is current stroking color non-white?
	visibleFill := true                                 // Is current non-stroking color non-nwhite?
	marked := false                                     // Has a mark been detected in the stream?
	markingPatterns := map[pdfcore.PdfObjectName]bool{} // List of already detected patterns. Re-use for subsequent detections.
	processedXObjects := map[string]bool{}              // List of already detected patterns. Re-use for subsequent detections.

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

			opMark := markingOperators[operand]
			if opMark.marking {
				hasArea := true
				switch operand {
				case "BI":
					marked = true
				case "Tj":
					if len(op.Params) < 1 {
						hasArea = false
						break
					}
					empty, err := emptyStringParam(op.Params[0])
					if err != nil {
						return err
					}
					if empty {
						hasArea = false
					}
				case "TJ":
					if len(op.Params) < 1 {
						hasArea = false
						break
					}
					empty, err := emptyArrayParam(op.Params[0])
					if err != nil {
						return err
					}
					if empty {
						hasArea = false
					}
				}

				if hasArea && ((visibleStroke && opMark.stroking) || (visibleFill && opMark.filling)) {
					marked = true
				}
				common.Log.Debug("op=%s opMark=%+v hasArea=%t marked=%t", op, opMark, hasArea, marked)
				return nil
			}

			switch operand {
			case "SC", "SCN": // Set stroking color.  Includes pattern colors.
				if isPatternCS(gs.ColorspaceStroking) {
					patternColor, ok := gs.ColorStroking.(*pdf.PdfColorPattern)
					if !ok {
						return errors.New("Invalid stroking color type")
					}

					if patternColor.Color != nil {
						visibleStroke = isColorMarking(patternColor.Color)
						common.Log.Debug("op=%s visibleStroke=%t", op, visibleStroke)
						return nil
					}

					if hasMarking, ok := markingPatterns[patternColor.PatternName]; ok {
						// Already processed, need not change anything, except underlying color if used.
						visibleStroke = hasMarking
						common.Log.Debug("op=%s visibleStroke=%t", op, visibleStroke)
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
					visibleStroke = hasMarking
					common.Log.Debug("op=%s visibleStroke=%t", op, visibleStroke)

				} else {
					visibleStroke := isColorMarking(gs.ColorStroking)
					common.Log.Debug("op=%s ColorspaceStroking=%T ColorStroking=%#v visibleStroke=%t",
						op, gs.ColorspaceStroking, gs.ColorStroking, visibleStroke)
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
						visibleFill = isColorMarking(patternColor.Color)
						common.Log.Debug("op=%s visibleFill=%t", op, visibleFill)
						return nil
					}
					if hasMarking, ok := markingPatterns[patternColor.PatternName]; ok {
						// Already processed, need not change anything, except underlying color if used.
						visibleFill = hasMarking
						common.Log.Debug("op=%s visibleFill=%t", op, visibleFill)
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
					markingPatterns[patternColor.PatternName] = hasMarking // !@#$ Fix color detection code
					visibleFill = hasMarking
					common.Log.Debug("op=%s visibleFill=%t", op, visibleFill)
				} else {
					hasMarking := isColorMarking(gs.ColorNonStroking)
					visibleFill = hasMarking
					common.Log.Debug("op=%s ColorspaceNonStroking=%T ColorNonStroking=%#v visibleFill=%t",
						op, gs.ColorspaceNonStroking, gs.ColorNonStroking, visibleFill)

				}
				return nil
			case "G", "RG", "K": // Set Gray, RGB or CMYK stroking color.
				visibleStroke = isColorMarking(gs.ColorStroking)
				common.Log.Debug("op=%s ColorspaceStroking=%T ColorStroking=%#v visibleStroke=%t",
					op, gs.ColorspaceStroking, gs.ColorStroking, visibleStroke)
				return nil
			case "g", "rg", "k": // Set Gray, RGB or CMYK as non-stroking color.
				visibleFill = isColorMarking(gs.ColorNonStroking)
				common.Log.Debug("op=%s ColorspaceStroking=%T ColorStroking=%#v visibleFill=%t",
					op, gs.ColorspaceStroking, gs.ColorStroking, visibleFill)
				return nil
				// case "sh": // Paints the shape and color defined by shading dict.
				// 	if len(op.Params) != 1 {
				// 		return errors.New("Params to sh operator should be 1")
				// 	}
				// 	_, ok := op.Params[0].(*pdfcore.PdfObjectName)
				// 	if !ok {
				// 		return errors.New("sh parameter should be a name")
				// 	}
				// 	marked = true
			}
			return nil
		})

	processor.AddHandler(pdfcontent.HandlerConditionEnumOperand, "Do",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState, resources *pdf.PdfPageResources) error {
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
				marked = true
				common.Log.Debug("image XObject name=%#q hasMarking=%t", *name, hasMarking)
				return nil

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

func emptyArrayParam(param pdfcore.PdfObject) (bool, error) {
	common.Log.Debug("emptyArrayParam: param=%s", param)
	arr, ok := param.(*pdfcore.PdfObjectArray)
	if !ok {
		return false, fmt.Errorf("Invalid parameter type, not array (%T)", param)
	}
	for _, p := range *arr {
		if empty, err := emptyStringParam(p); err != nil || !empty {
			return empty, err
		}
	}
	common.Log.Debug("emptyArrayParam: EMPTY!!")
	return true, nil
}

func emptyStringParam(param pdfcore.PdfObject) (bool, error) {
	text, ok := param.(*pdfcore.PdfObjectString)
	if !ok {
		return false, fmt.Errorf("Invalid parameter type, not string (%T)", param)
	}
	trimmed := stripCtlFromUTF8(string(*text))
	empty := len(trimmed) == 0
	common.Log.Debug("emptyStringParam: empty=%t len=%d text='%s' trimmed=%#q=%+v",
		empty, len(trimmed), text, trimmed, []byte(trimmed))
	return empty, nil
}

func stripCtlFromUTF8(str string) string {
	return strings.Map(func(r rune) rune {
		if r > 32 && r != 127 {
			return r
		}
		return -1
	}, str)
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
