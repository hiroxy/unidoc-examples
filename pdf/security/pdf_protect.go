/*
 * Protects PDF files by setting a password on it. This example both sets user
 * and opening password and hard-codes the protection bits here, but easily adjusted
 * in the code here although not on the command line.
 *
 * When reading the input it tries to decrypt with empty password if the input file
 * is encrypted, if that fails we fail also.
 *
 * Run as: go run pdf_protect.go input.pdf password output.pdf
 * e.g.: go run pdf_protect.go my.pdf mypass my_protected.pdf
 */

package main

import (
	"fmt"
	"os"

	pdfcore "github.com/unidoc/unidoc/pdf/core"
	pdf "github.com/unidoc/unidoc/pdf/model"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Printf("Usage: go run pdf_protect.go input.pdf password output.pdf\n")
		os.Exit(1)
	}

	inputPath := os.Args[1]
	password := os.Args[2]
	outputPath := os.Args[3]

	err := protectPdf(inputPath, outputPath, password)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Complete, see output file: %s\n", outputPath)
}

func protectPdf(inputPath string, outputPath string, password string) error {
	pdfWriter := pdf.NewPdfWriter()

	// Feel free to change these values when testing.
	allowPrinting := false
	allowModifications := true
	allowCopying := true
	allowForm := false

	permissions := pdfcore.AccessPermissions{}
	permissions.Printing = allowPrinting
	permissions.Modify = allowModifications
	permissions.Annotate = allowModifications
	permissions.RotateInsert = allowModifications
	permissions.ExtractGraphics = allowCopying
	permissions.DisabilityExtract = allowCopying
	permissions.FillForms = allowForm
	permissions.LimitPrintQuality = false

	encryptOptions := &pdf.EncryptOptions{}
	encryptOptions.Permissions = permissions

	err := pdfWriter.Encrypt([]byte(password), []byte(password), encryptOptions)
	if err != nil {
		return err
	}

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

	// Try decrypting both with given password and an empty one if that fails.
	if isEncrypted {
		auth, err := pdfReader.Decrypt([]byte(password))
		if err != nil {
			return err
		}
		if !auth {
			return fmt.Errorf("Wrong password")
		}
	}

	numPages, err := pdfReader.GetNumPages()
	if err != nil {
		return err
	}

	for i := 0; i < numPages; i++ {
		pageNum := i + 1

		page, err := pdfReader.GetPage(pageNum)
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
