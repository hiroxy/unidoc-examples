/*
 * Detect the number of pages and the color pages (1-offset) all pages in a list of PDF files.
 * Compares these results to running Ghostscript on the PDF files and reports an error if the results don't match.
 *
 * Run as: ./pdf_describe -o output [-d] [-a] testdata/*.pdf > blah
 *
 * The main results are written to stderr so you will see them in your console.
 * Detailed information is written to stdout and you will see them in blah.
 *
 *  See the other command line options in the top of main()
 *      -d Write debug level logs to stdout
 *		-a Tests all the input files. The default behavior is stop at the first failure. Use this
 *			to find out how many of your corpus files this program works for.
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

func initUniDoc(debug bool) {
	logLevel := common.LogLevelInfo
	if debug {
		logLevel = common.LogLevelDebug
	}
	common.SetLogger(common.ConsoleLogger{LogLevel: logLevel})
}

const usage = `Usage:
pdf_analyze <file>
-d: Debug level logging
`

func main() {
	debug := false // Write debug level info to stdout?

	flag.BoolVar(&debug, "d", false, "Enable debug logging")
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	initUniDoc(debug)

	inputPath := args[0]

	numPages, colorPages, width, height, err := describePdf(inputPath)
	if err != nil {
		common.Log.Error("describePdf failed. err=%v", err)
		os.Exit(1)
	}
	if width <= 1.0 || height <= 1.0 {
		common.Log.Error("Width, Height not specified")
		os.Exit(1)
	}

	err = writeAnalyis(Analysis{
		NumPages:   numPages,
		Width:      width,
		Height:     height,
		ColorPages: colorPages,
	})
	if err != nil {
		common.Log.Error("describePdf failed. err=%v", err)
		os.Exit(1)
	}
}

// describePdf reads PDF `inputPath` and returns number of pages, slice of color page numbers (1-offset)
func describePdf(inputPath string) (int, []int, float64, float64, error) {

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

	colorPages := []int{}
	var width, height float64

	for i := 0; i < numPages; i++ {
		pageNum := i + 1
		page := pdfReader.PageList[i]
		common.Log.Debug("page %d", pageNum)

		w, h, _ := pageSize(page)
		if w > width {
			width = w
		}
		if h > height {
			height = h
		}

		desc := fmt.Sprintf("%s:page%d", filepath.Base(inputPath), pageNum)
		colored, err := isPageColored(page, desc, false)
		if err != nil {
			return numPages, colorPages, width, height, err
		}
		if colored {
			colorPages = append(colorPages, pageNum)
		}
	}

	return numPages, colorPages, width, height, nil
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

// isPageColored returns true if `page` contains color. It also references
// XObject Images and Forms to _possibly_ record if they contain color
func isPageColored(page *pdf.PdfPage, desc string, debug bool) (bool, error) {
	// For each page, we go through the resources and look for the images.
	resources, err := page.GetResources()
	if err != nil {
		common.Log.Error("GetResources failed. err=%v", err)
		return false, err
	}

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

	colored, err := isContentStreamColored(contents, resources, debug)
	if debug {
		common.Log.Info("colored=%t err=%v", colored, err)
	}
	if err != nil {
		common.Log.Error("isContentStreamColored failed. err=%v", err)
		return false, err
	}
	return colored, nil
}

// isPatternCS returns true if `colorspace` represents a Pattern colorspace.
func isPatternCS(cs pdf.PdfColorspace) bool {
	_, isPattern := cs.(*pdf.PdfColorspaceSpecialPattern)
	return isPattern
}

// isContentStreamColored returns true if `contents` contains any color object
func isContentStreamColored(contents string, resources *pdf.PdfPageResources, debug bool) (bool, error) {
	cstreamParser := pdfcontent.NewContentStreamParser(contents)
	operations, err := cstreamParser.Parse()
	if err != nil {
		return false, err
	}

	colored := false                                    // Has a colored mark been detected in the stream?
	coloredPatterns := map[pdfcore.PdfObjectName]bool{} // List of already detected patterns. Re-use for subsequent detections.
	coloredShadings := map[string]bool{}                // List of already detected shadings. Re-use for subsequent detections.

	// The content stream processor keeps track of the graphics state and we can make our own handlers to process
	// certain commands using the AddHandler method. In this case, we hook up to color related operands, and for image
	// and form handling.
	processor := pdfcontent.NewContentStreamProcessor(*operations)
	// Add handlers for colorspace related functionality.
	processor.AddHandler(pdfcontent.HandlerConditionEnumAllOperands, "",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState,
			resources *pdf.PdfPageResources) error {
			if colored {
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
						if isColorColored(patternColor.Color) {
							if debug {
								common.Log.Info("op=%s hasCol=%t", op, true)
							}
							colored = true
							return nil
						}
					}

					if hasCol, ok := coloredPatterns[patternColor.PatternName]; ok {
						// Already processed, need not change anything, except underlying color if used.
						if hasCol {
							if debug {
								common.Log.Info("op=%s hasCol=%t", op, hasCol)
							}
							colored = true
						}
						return nil
					}

					// Look up the pattern name and convert it.
					pattern, found := resources.GetPatternByName(patternColor.PatternName)
					if !found {
						return errors.New("Undefined pattern name")
					}
					hasCol, err := isPatternColored(pattern, debug)
					if err != nil {
						common.Log.Error("isPatternColored failed. err=%v", err)
						return err
					}
					coloredPatterns[patternColor.PatternName] = hasCol
					colored = colored || hasCol
					if debug {
						common.Log.Info("op=%s hasCol=%t", op, hasCol)
					}

				} else {
					hasCol := isColorColored(gs.ColorStroking)
					colored = colored || hasCol
					if debug {
						common.Log.Info("op=%s ColorspaceStroking=%T ColorStroking=%#v hasCol=%t",
							op, gs.ColorspaceStroking, gs.ColorStroking, hasCol)
					}
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
						hasCol := isColorColored(patternColor.Color)
						colored = colored || hasCol
						if debug {
							common.Log.Info("op=%#v hasCol=%t", op, hasCol)
						}
					}
					if hasCol, ok := coloredPatterns[patternColor.PatternName]; ok {
						// Already processed, need not change anything, except underlying color if used.
						colored = colored || hasCol
						if debug {
							common.Log.Info("op=%#v hasCol=%t", op, hasCol)
						}
						return nil
					}

					// Look up the pattern name and convert it.
					pattern, found := resources.GetPatternByName(patternColor.PatternName)
					if !found {
						return errors.New("Undefined pattern name")
					}
					hasCol, err := isPatternColored(pattern, debug)
					if err != nil {
						common.Log.Debug("Unable to convert pattern to grayscale: %v", err)
						return err
					}
					coloredPatterns[patternColor.PatternName] = hasCol
				} else {
					hasCol := isColorColored(gs.ColorNonStroking)
					colored = colored || hasCol
					if debug {
						common.Log.Info("op=%s ColorspaceNonStroking=%T ColorNonStroking=%#v hasCol=%t",
							op, gs.ColorspaceNonStroking, gs.ColorNonStroking, hasCol)
					}

				}
				return nil
			case "RG", "K": // Set RGB or CMYK stroking color.
				hasCol := isColorColored(gs.ColorStroking)
				if debug {
					common.Log.Info("op=%s ColorspaceStroking=%T ColorStroking=%#v hasCol=%t",
						op, gs.ColorspaceStroking, gs.ColorStroking, hasCol)
				}
				colored = colored || hasCol
				return nil
			case "rg", "k": // Set RGB or CMYK as non-stroking color.
				hasCol := isColorColored(gs.ColorNonStroking)
				colored = colored || hasCol
				if debug {
					common.Log.Info("op=%s ColorspaceStroking=%T ColorStroking=%#v hasCol=%t",
						op, gs.ColorspaceStroking, gs.ColorStroking, hasCol)
				}
				return nil
			case "sh": // Paints the shape and color defined by shading dict.
				if len(op.Params) != 1 {
					return errors.New("Params to sh operator should be 1")
				}
				shname, ok := op.Params[0].(*pdfcore.PdfObjectName)
				if !ok {
					return errors.New("sh parameter should be a name")
				}
				if hasCol, has := coloredShadings[string(*shname)]; has {
					// Already processed, no need to do anything.
					colored = colored || hasCol
					if debug {
						common.Log.Info("hasCol=%t", hasCol)
					}
					return nil
				}

				shading, found := resources.GetShadingByName(*shname)
				if !found {
					common.Log.Error("Shading not defined in resources. shname=%#q", string(*shname))
					return errors.New("Shading not defined in resources")
				}
				hasCol, err := isShadingColored(shading)
				if err != nil {
					return err
				}
				coloredShadings[string(*shname)] = hasCol
			}
			return nil
		})

	// Add handler for image related handling.  Note that inline images are completely stored with a ContentStreamInlineImage
	// object as the parameter for BI.
	processor.AddHandler(pdfcontent.HandlerConditionEnumOperand, "BI",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState, resources *pdf.PdfPageResources) error {
			if colored {
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
			if debug {
				common.Log.Info("iimg=%s", iimg)
			}
			img, err := iimg.ToImage(resources)
			if err != nil {
				common.Log.Error("Error converting inline image to image: %v", err)
				return err
			}

			if debug {
				common.Log.Info("img=%v %d", img.ColorComponents, img.BitsPerComponent)
			}

			if img.ColorComponents <= 1 {
				return nil
			}

			cs, err := iimg.GetColorSpace(resources)
			if err != nil {
				common.Log.Error("Error getting color space for inline image: %v", err)
				return err
			}
			rgbImg, err := cs.ImageToRGB(*img)
			if err != nil {
				common.Log.Error("Error converting image to rgb: %v", err)
				return err
			}
			hasCol := isRgbImageColored(rgbImg, debug)
			colored = colored || hasCol
			if debug {
				common.Log.Info("hasCol=%t", hasCol)
			}

			return nil
		})

	// Handler for XObject Image and Forms.
	processedXObjects := map[string]bool{} // Keep track of processed XObjects to avoid repetition.

	processor.AddHandler(pdfcontent.HandlerConditionEnumOperand, "Do",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState, resources *pdf.PdfPageResources) error {
			if colored {
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
			hasCol, has := processedXObjects[string(*name)]
			common.Log.Debug("name=%q has=%t hasCol=%t processedXObjects=%+v", *name, has, hasCol, processedXObjects)
			if has {
				colored = colored || hasCol
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
				if debug {
					common.Log.Info("!!Filter=%s ColorSpace=%s ImageMask=%v wxd=%dx%d",
						ximg.Filter.GetFilterName(), ximg.ColorSpace,
						ximg.ImageMask, *ximg.Width, *ximg.Height)
				}
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
					colored = true
					return nil
				// These filters are only used with grayscale images
				case "CCITTDecode", "JBIG2Decode":
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

				if debug {
					common.Log.Info("img: ColorComponents=%d wxh=%dx%d", img.ColorComponents, img.Width, img.Height)
					common.Log.Info("ximg: ColorSpace=%T=%s mask=%v", ximg.ColorSpace, ximg.ColorSpace, ximg.Mask)
					common.Log.Info("rgbImg: ColorComponents=%d wxh=%dx%d", rgbImg.ColorComponents, rgbImg.Width, rgbImg.Height)
				}

				hasCol := isRgbImageColored(rgbImg, debug)
				processedXObjects[string(*name)] = hasCol
				colored = colored || hasCol
				if debug {
					common.Log.Info("hasCol=%t", hasCol)
				}

			} else if xtype == pdf.XObjectTypeForm {
				common.Log.Debug(" XObject Form: %s", *name)

				// Go through the XObject Form content stream.
				xform, err := resources.GetXObjectFormByName(*name)
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				formContent, err := xform.GetContentStream()
				if err != nil {
					common.Log.Error("err=%v")
					return err
				}

				// Process the content stream in the Form object too:
				// XXX/TODO/Consider: Use either form resources (priority) and fall back to page resources alternatively if not found.
				// Have not come into cases where needed yet.
				formResources := xform.Resources
				if formResources == nil {
					formResources = resources
				}

				// Process the content stream in the Form object too:
				hasCol, err := isContentStreamColored(string(formContent), formResources, debug)
				if err != nil {
					common.Log.Error("err=%v", err)
					return err
				}
				processedXObjects[string(*name)] = hasCol
				colored = colored || hasCol
				if debug {
					common.Log.Info("hasCol=%t", hasCol)
				}

			}

			return nil
		})

	err = processor.Process(resources)
	if err != nil {
		common.Log.Error("processor.Process returned: err=%v", err)
		return false, err
	}

	return colored, nil
}

// isPatternColored returns true if `pattern` contains color (tiling or shading pattern).
func isPatternColored(pattern *pdf.PdfPattern, debug bool) (bool, error) {
	// Case 1: Colored tiling patterns.  Need to process the content stream and replace.
	if pattern.IsTiling() {
		tilingPattern := pattern.GetAsTilingPattern()
		if tilingPattern.IsColored() {
			// A colored tiling pattern can use color operators in its stream, need to process the stream.
			content, err := tilingPattern.GetContentStream()
			if err != nil {
				return false, err
			}
			colored, err := isContentStreamColored(string(content), tilingPattern.Resources, debug)
			return colored, err
		}
	} else if pattern.IsShading() {
		// Case 2: Shading patterns.  Need to create a new colorspace that can map from N=3,4 colorspaces to grayscale.
		shadingPattern := pattern.GetAsShadingPattern()
		colored, err := isShadingColored(shadingPattern.Shading)
		return colored, err
	}
	common.Log.Error("isPatternColored. pattern is neither tiling nor shading")
	return false, nil
}

// isShadingColored returns true if `shading` is a colored colorspace
func isShadingColored(shading *pdf.PdfShading) (bool, error) {
	cs := shading.ColorSpace
	if cs.GetNumComponents() == 1 {
		// Grayscale colorspace
		return false, nil
	} else if cs.GetNumComponents() == 3 {
		// RGB colorspace
		return true, nil
	} else if cs.GetNumComponents() == 4 {
		// CMYK colorspace
		return true, nil
	} else {
		err := errors.New("Unsupported pattern colorspace for color detection")
		common.Log.Error("isShadingColored: colorpace N=%d err=%v", cs.GetNumComponents(), err)
		return false, err
	}
}

// isColorColored returns true if `color` is not gray
func isColorColored(color pdf.PdfColor) bool {
	switch color.(type) {
	case *pdf.PdfColorDeviceGray:
		return false
	case *pdf.PdfColorDeviceRGB:
		col := color.(*pdf.PdfColorDeviceRGB)
		r, g, b := col.R(), col.G(), col.B()
		return visible(r-g, r-b, g-b)
	case *pdf.PdfColorDeviceCMYK:
		col := color.(*pdf.PdfColorDeviceCMYK)
		c, m, y := col.C(), col.M(), col.Y()
		return visible(c-m, c-y, m-y)
	case *pdf.PdfColorCalGray:
		return false
	case *pdf.PdfColorCalRGB:
		col := color.(*pdf.PdfColorCalRGB)
		a, b, c := col.A(), col.B(), col.C()
		return visible(a-b, a-c, b-c)
	case *pdf.PdfColorLab:
		col := color.(*pdf.PdfColorLab)
		a, b := col.A(), col.B()
		return visible(a, b)
	}
	common.Log.Error("isColorColored: Unknown color %T %s", color, color)
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
		if visible(r-g, r-b, g-b) {
			if debug {
				common.Log.Info("@@ colored pixel: i=%d rgb=%.3f %.3f %.3f", i, r, g, b)
				common.Log.Info("                 delta rgb=%.3f %.3f %.3f", r-g, r-b, g-b)
				common.Log.Info("            colorTolerance=%.3f", colorTolerance)
			}
			return true
		}
	}
	return false
}

// ColorTolerance is the smallest color component that is visible on a typical mid-range color laser printer
// cpts have values in range 0.0-1.0
const colorTolerance = 3.1 / 255.0

// visible returns true if any of color component `cpts` is visible on a typical mid-range color laser printer
// cpts have values in range 0.0-1.0
func visible(cpts ...float64) bool {
	for _, x := range cpts {
		if math.Abs(x) > colorTolerance {
			return true
		}
	}
	return false
}

type Analysis struct {
	NumPages   int
	Width      float64
	Height     float64
	ColorPages []int
}

func writeAnalyis(a Analysis) error {
	b, err := json.Marshal(a)
	if err != nil {
		common.Log.Error("writeAnalyis: err=%v", err)
		return err
	}
	fmt.Printf("%s\n", b)
	return nil
}
