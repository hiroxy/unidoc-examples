/*
 * Convert PDF including images and content stream data to grayscale.
 *
 * This example demonstrates some of the more complex capabilities of UniDoc, showing the capability to process and transform
 * objects and contents.
 *
 * Run as: go run pdf_grayscale_transform.go input.pdf output.pdf
 */

package main

import (
	"errors"
	"fmt"
	"os"

	common "github.com/unidoc/unidoc/common"
	pdfcontent "github.com/unidoc/unidoc/pdf/contentstream"
	pdfcore "github.com/unidoc/unidoc/pdf/core"
	pdf "github.com/unidoc/unidoc/pdf/model"
)

func init() {
	// To make the library log we just have to initialise the logger which satisfies
	// the unicommon.Logger interface, unicommon.DummyLogger is the default and
	// does not do anything. Very easy to implement your own.
	//common.SetLogger(common.DummyLogger{})
	common.SetLogger(common.NewConsoleLogger(common.LogLevelDebug))
	// common.SetLogger(common.NewConsoleLogger(common.LogLevelTrace))
}

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("Syntax: go run pdf_grayscale_transform.go input.pdf output.pdf\n")
		os.Exit(1)
	}

	inputPath := os.Args[1]
	outputPath := os.Args[2]

	err := convertPdfToGrayscale(inputPath, outputPath)
	if err != nil {
		fmt.Printf("Failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Completed, see output %s\n", outputPath)
}

func convertPdfToGrayscale(inputPath, outputPath string) error {
	pdfWriter := pdf.NewPdfWriter()

	f, err := os.Open(inputPath)
	if err != nil {
		return err
	}

	defer f.Close()

	pdfReader, err := pdf.NewPdfReader(f)
	if err != nil {
		return err
	}

	isEncrypted, err := pdfReader.IsEncrypted()
	if err != nil {
		return err
	}

	// Try decrypting with an empty one.
	if isEncrypted {
		auth, err := pdfReader.Decrypt([]byte(""))
		if err != nil {
			// Encrypted and we cannot do anything about it.
			return err
		}
		if !auth {
			return errors.New("Need to decrypt with password")
		}
	}

	numPages, err := pdfReader.GetNumPages()
	if err != nil {
		return err
	}
	fmt.Printf("PDF Num Pages: %d\n", numPages)

	for i := 0; i < numPages; i++ {
		page, err := pdfReader.GetPage(i + 1)
		if err != nil {
			return err
		}

		err = convertPageToGrayscale(page)
		if err != nil {
			return err
		}

		err = pdfWriter.AddPage(page)
		if err != nil {
			return err
		}
	}

	fWrite, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer fWrite.Close()

	err = pdfWriter.Write(fWrite)
	if err != nil {
		return err
	}

	return nil
}

// Replaces color objects on the page with grayscale ones.  Also references XObject Images and Forms
// to convert those to grayscale.
func convertPageToGrayscale(page *pdf.PdfPage) error {
	// For each page, we go through the resources and look for the images.
	resources, err := page.GetResources()
	if err != nil {
		return err
	}

	contents, err := page.GetAllContentStreams()
	if err != nil {
		return err
	}

	grayContent, err := transformContentStreamToGrayscale(contents, resources)
	if err != nil {
		return err
	}
	page.SetContentStreams([]string{string(grayContent)}, pdfcore.NewFlateEncoder())

	fmt.Printf("Processed contents: %s\n", grayContent)

	return nil
}

func transformContentStreamToGrayscale(contents string, resources *pdf.PdfPageResources) ([]byte, error) {
	cstreamParser := pdfcontent.NewContentStreamParser(contents)
	operations, err := cstreamParser.Parse()
	if err != nil {
		return nil, err
	}
	processedOperations := &pdfcontent.ContentStreamOperations{}

	// The content stream processor keeps track of the graphics state and we can make our own handlers to process certain commands,
	// using the AddHandler method.  In this case, we hook up to color related operands, and for image and form handling.
	processor := pdfcontent.NewContentStreamProcessor(operations)
	// Add handlers for colorspace related functionality.
	processor.AddHandler(pdfcontent.HandlerConditionEnumAllOperands, "",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState, resources *pdf.PdfPageResources) error {
			operand := op.Operand
			switch operand {
			case "CS", "cs": // Set colorspace operands.
				op := pdfcontent.ContentStreamOperation{}
				op.Operand = operand
				op.Params = []pdfcore.PdfObject{pdfcore.MakeName("DeviceGray")}
				*processedOperations = append(*processedOperations, &op)
				return nil
			case "SC", "SCN": // Set stroking color.
				color, err := gs.ColorspaceStroking.ColorToRGB(gs.ColorStroking)
				if err != nil {
					fmt.Printf("Error: %v\n", err)
					return err
				}
				rgbColor := color.(*pdf.PdfColorDeviceRGB)
				grayColor := rgbColor.ToGray()

				op := pdfcontent.ContentStreamOperation{}
				op.Operand = operand
				op.Params = []pdfcore.PdfObject{pdfcore.MakeFloat(grayColor.Val())}

				*processedOperations = append(*processedOperations, &op)
				return nil
			case "sc", "scn": // Set nonstroking color.
				color, err := gs.ColorspaceNonStroking.ColorToRGB(gs.ColorNonStroking)
				if err != nil {
					fmt.Printf("Error: %v\n", err)
					return err
				}
				rgbColor := color.(*pdf.PdfColorDeviceRGB)
				grayColor := rgbColor.ToGray()

				op := pdfcontent.ContentStreamOperation{}
				op.Operand = operand
				op.Params = []pdfcore.PdfObject{pdfcore.MakeFloat(grayColor.Val())}

				*processedOperations = append(*processedOperations, &op)
				return nil
			case "RG", "K": // Set RGB or CMYK stroking color.
				color, err := gs.ColorspaceStroking.ColorToRGB(gs.ColorStroking)
				if err != nil {
					fmt.Printf("Error: %v\n", err)
					return err
				}
				rgbColor := color.(*pdf.PdfColorDeviceRGB)
				grayColor := rgbColor.ToGray()

				op := pdfcontent.ContentStreamOperation{}
				op.Operand = "G"
				op.Params = []pdfcore.PdfObject{pdfcore.MakeFloat(grayColor.Val())}

				*processedOperations = append(*processedOperations, &op)
				return nil
			case "rg", "k": // Set RGB or CMYK as nonstroking color.
				color, err := gs.ColorspaceNonStroking.ColorToRGB(gs.ColorNonStroking)
				if err != nil {
					fmt.Printf("Error: %v\n", err)
					return err
				}
				rgbColor := color.(*pdf.PdfColorDeviceRGB)
				grayColor := rgbColor.ToGray()

				op := pdfcontent.ContentStreamOperation{}
				op.Operand = "g"
				op.Params = []pdfcore.PdfObject{pdfcore.MakeFloat(grayColor.Val())}

				*processedOperations = append(*processedOperations, &op)
				return nil
			}
			*processedOperations = append(*processedOperations, op)

			return nil
		})
	// Add handler for image related handling.  Note that inline images are completely stored with a ContentStreamInlineImage
	// object as the parameter for BI.
	processor.AddHandler(pdfcontent.HandlerConditionEnumOperand, "BI",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState, resources *pdf.PdfPageResources) error {
			if len(op.Params) != 1 {
				fmt.Printf("BI Error invalid number of params\n")
				return errors.New("invalid number of parameters")
			}
			// Inline image.
			iimg, ok := op.Params[0].(*pdfcontent.ContentStreamInlineImage)
			if !ok {
				fmt.Printf("Error: Invalid handling for inline image\n")
				return errors.New("Invalid inline image parameter")
			}

			img, err := iimg.ToImage(resources)
			if err != nil {
				fmt.Printf("Error converting inline image to image: %v\n", err)
				return err
			}

			cs, err := iimg.GetColorSpace(resources)
			if err != nil {
				fmt.Printf("Error getting color space for inline image: %v\n", err)
				return err
			}
			rgbImg, err := cs.ImageToRGB(*img)
			if err != nil {
				fmt.Printf("Error converting image to rgb: %v\n", err)
				return err
			}
			rgbColorSpace := pdf.NewPdfColorspaceDeviceRGB()
			grayImage, err := rgbColorSpace.ImageToGray(rgbImg)
			if err != nil {
				fmt.Printf("Error converting img to gray: %v\n", err)
				return err
			}
			grayInlineImg, err := pdfcontent.NewInlineImageFromImage(grayImage, nil)
			if err != nil {
				fmt.Printf("Error making a new inline image object: %v\n", err)
				return err
			}

			// Replace inline image data with the gray image.
			pOp := pdfcontent.ContentStreamOperation{}
			pOp.Operand = "BI"
			pOp.Params = []pdfcore.PdfObject{grayInlineImg}
			*processedOperations = append(*processedOperations, &pOp)

			return nil
		})

	// Handler for XObject Image and Forms.
	processedXObjects := map[string]bool{} // Keep track of processed XObjects to avoid repetition.

	processor.AddHandler(pdfcontent.HandlerConditionEnumOperand, "Do",
		func(op *pdfcontent.ContentStreamOperation, gs pdfcontent.GraphicsState, resources *pdf.PdfPageResources) error {
			operand := op.Operand
			fmt.Printf("Do handler: %s\n", operand)
			if len(op.Params) < 1 {
				fmt.Printf("ERROR: Invalid number of params for Do object.\n")
				return errors.New("Range check")
			}

			// XObject.
			name := op.Params[0].(*pdfcore.PdfObjectName)

			// Only process each one once.
			_, has := processedXObjects[string(*name)]
			if has {
				return nil
			}
			processedXObjects[string(*name)] = true

			_, xtype := resources.GetXObjectByName(string(*name))
			if xtype == pdf.XObjectTypeImage {
				fmt.Printf(" XObject Image: %s\n", *name)

				ximg, err := resources.GetXObjectImageByName(string(*name))
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				img, err := ximg.ToImage()
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				rgbImg, err := ximg.ColorSpace.ImageToRGB(*img)
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				rgbColorSpace := pdf.NewPdfColorspaceDeviceRGB()
				grayImage, err := rgbColorSpace.ImageToGray(rgbImg)
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				// Update the XObject image.
				err = ximg.SetImage(&grayImage, nil)
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				// Update the container.
				_ = ximg.ToPdfObject()
			} else if xtype == pdf.XObjectTypeForm {
				// Go through the XObject Form content stream.
				xform, err := resources.GetXObjectFormByName(string(*name))
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				formContent, err := xform.GetContentStream()
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				// Process the content stream in the Form object too:
				// XXX/TODO: Use either form resources (priority) and fall back to page resources alternatively if not found.
				formResources := xform.FormResources
				if formResources == nil {
					formResources = resources
				}

				// Process the content stream in the Form object too:
				grayContent, err := transformContentStreamToGrayscale(string(formContent), formResources)
				if err != nil {
					fmt.Printf("Error : %v\n", err)
					return err
				}

				xform.SetContentStream(grayContent)
				// Update the container.
				_ = xform.ToPdfObject()
			}

			return nil
		})

	err = processor.Process(resources)
	if err != nil {
		fmt.Printf("Error processing: %v\n", err)
		return nil, err
	}

	// For debug purposes: (high level logging).
	fmt.Printf("=== Unprocessed - Full list\n")
	for idx, op := range operations {
		fmt.Printf("U. Operation %d: %s - Params: %v\n", idx+1, op.Operand, op.Params)
	}
	fmt.Printf("=== Processed - Full list\n")
	for idx, op := range *processedOperations {
		fmt.Printf("P. Operation %d: %s - Params: %v\n", idx+1, op.Operand, op.Params)
	}

	return processedOperations.Bytes(), nil
}
