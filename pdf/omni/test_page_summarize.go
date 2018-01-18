/*
 * Test pdf_page_summaries.go
 *
 * Compares its
 * results to running Ghostscript on the PDF files and reports an error if the results don't match.
 *
 * Run as: ./pdf_page_summaries -o output [-d] [-a] testdata/*.pdf > blah
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
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"time"

	common "github.com/unidoc/unidoc/common"
)

const usage = `Usage:
pdf_page_summaries -o <output directory> [-d][-a][-min <val>][-max <val>] <file1> <file2> ...
-d: Debug level logging
-a: Keep converting PDF files after failures
-min <val>: Minimum PDF file size to test
-max <val>: Maximum PDF file size to test
-r <name>: Name of results file
`

func initUniDoc(debug bool) {
	logLevel := common.LogLevelInfo
	if debug {
		logLevel = common.LogLevelDebug
	}
	common.SetLogger(common.ConsoleLogger{LogLevel: logLevel})
}

func main() {
	fmt.Println("=======================================================================")
	debug := false            // Write debug level info to stdout?
	keep := false             // Keep the rasters used for PDF comparison"
	compareGrayscale := false // Do PDF raster comparison on grayscale rasters?
	runAllTests := false      // Don't stop when a PDF file fails to process?
	var minSize int64 = -1    // Minimum size for an input PDF to be processed.
	var maxSize int64 = -1    // Maximum size for an input PDF to be processed.
	var results string        // Results file
	flag.BoolVar(&debug, "d", false, "Enable debug logging")
	flag.BoolVar(&keep, "k", false, "Keep temp files")
	flag.BoolVar(&compareGrayscale, "g", false, "Do PDF raster comparison on grayscale rasters")
	flag.BoolVar(&runAllTests, "a", false, "Run all tests. Don't stop at first failure")
	flag.Int64Var(&minSize, "min", -1, "Minimum size of files to process (bytes)")
	flag.Int64Var(&maxSize, "max", -1, "Maximum size of files to process (bytes)")
	flag.StringVar(&results, "r", "", "Results file")

	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	initUniDoc(debug)

	compDir := makeUniqueDir("compare.pdfs")
	fmt.Fprintf(os.Stderr, "compDir=%#q\n", compDir)
	if !keep {
		defer removeDir(compDir)
	}

	writers := []io.Writer{os.Stderr}
	if len(results) > 0 {
		f, err := os.OpenFile(results, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0777)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		writers = append(writers, f)
	}

	pdfList, err := patternsToPaths(args)
	if err != nil {
		common.Log.Error("patternsToPaths failed. args=%#q err=%v", args, err)
		os.Exit(1)
	}
	pdfList = sortFiles(pdfList, minSize, maxSize)
	passFiles := []string{}
	badFiles := []string{}
	failFiles := []string{}

	for idx, inputPath := range pdfList {

		_, name := filepath.Split(inputPath)
		inputSize := fileSize(inputPath)
		report(writers, "%3d of %d %#-30q  (%6d)", idx, len(pdfList), name, inputSize)

		result := "pass"
		t0 := time.Now()
		numPages, markedPagesPredicted, err := runPdfPage(inputPath)
		dt := time.Since(t0)
		if err != nil {
			common.Log.Error("runPdfPage failed. err=%v", err)
			result = "bad"
		}
		report(writers, " %d pages %d marked %.3f sec", numPages, len(markedPagesPredicted), dt.Seconds())

		if result == "pass" {
			markedPagesActual, err := gsMarkedPages(inputPath, compDir, keep)

			if err != nil || !equalSlices(markedPagesActual, markedPagesPredicted) {
				if err != nil {
					common.Log.Error("PDF is damaged. err=%v\n\tinputPath=%#q", err, inputPath)
				} else {
					common.Log.Error("Mismatch markedPages: \nActual   =%d %v\nPredicted=%d %v",
						len(markedPagesActual), markedPagesActual,
						len(markedPagesPredicted), markedPagesPredicted)
					fp := sliceDiff(markedPagesPredicted, markedPagesActual)
					fn := sliceDiff(markedPagesActual, markedPagesPredicted)
					if len(fp) > 0 {
						common.Log.Error("False positives=%d %+v", len(fp), fp)
					}
					if len(fn) > 0 {
						common.Log.Error("False negatives=%d %+v", len(fn), fn)
					}
				}
				result = "fail"
			}
		}
		report(writers, ", %s\n", result)

		switch result {
		case "pass":
			passFiles = append(passFiles, inputPath)
		case "fail":
			failFiles = append(failFiles, inputPath)
		case "bad":
			badFiles = append(badFiles, inputPath)
		}

		if result != "pass" {
			if runAllTests {
				continue
			}
			break
		}

	}

	report(writers, "%d files %d bad %d pass %d fail\n", len(pdfList), len(badFiles), len(passFiles), len(failFiles))
	report(writers, "%d bad\n", len(badFiles))
	for i, path := range badFiles {
		report(writers, "%3d %#q\n", i, path)
	}
	report(writers, "%d pass\n", len(passFiles))
	for i, path := range passFiles {
		report(writers, "%3d %#q\n", i, path)
	}
	report(writers, "%d fail\n", len(failFiles))
	for i, path := range failFiles {
		report(writers, "%3d %#q\n", i, path)
	}
}

// report writes Sprintf formatted `format` ... to all writers in `writers`
func report(writers []io.Writer, format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	for _, w := range writers {
		if _, err := io.WriteString(w, msg); err != nil {
			common.Log.Error("report: write to %#v failed msg=%s err=%v", w, msg, err)
		}
	}
}

// equalSlices returns true if `a` and `b` are identical
func equalSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i, x := range a {
		if x != b[i] {
			return false
		}
	}
	return true
}

// runPdfPage reads PDF `pdf` and returns number of pages, slice of marked page numbers (1-offset)
func runPdfPage(pdf string) (int, []int, error) {

	cmd := exec.Command(
		"./pdf_page_summaries",
		pdf)
	common.Log.Debug("runPdfPage: cmd=%#q", cmd.Args)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		common.Log.Error("runPdfPage: Could not process pdf=%q err=%v\nstdout=<%s>\nstderr=<%s>",
			pdf, err, stdout.String(), stderr.String())
		return 0, nil, err
	}
	summary, err := readSummary(stdout.Bytes())
	if err != nil {
		return 0, nil, err
	}
	common.Log.Debug("summary=%+v", summary)
	return summary.NumPages, summary.MarkedPages, nil
}

// sortFiles returns the paths of the files in `pathList` sorted by ascending size.
// If minSize > 0 then only files of this size or larger are returned.
// If maxSize > 0 then only files of this size or smaller are returned.
func sortFiles(pathList []string, minSize, maxSize int64) []string {
	n := len(pathList)
	fdList := make([]FileData, n)
	for i, path := range pathList {
		fi, err := os.Stat(path)
		if err != nil {
			panic(err)
		}
		fdList[i].path = path
		fdList[i].FileInfo = fi
	}

	sort.Stable(byFile(fdList))

	i0 := 0
	i1 := n
	if minSize >= 0 {
		i0 = sort.Search(len(fdList), func(i int) bool { return fdList[i].Size() >= minSize })
	}
	if maxSize >= 0 {
		i1 = sort.Search(len(fdList), func(i int) bool { return fdList[i].Size() >= maxSize })
	}
	fdList = fdList[i0:i1]

	outList := make([]string, len(fdList))
	for i, fd := range fdList {
		outList[i] = fd.path
	}

	return outList
}

type FileData struct {
	path string
	os.FileInfo
}

// byFile sorts slices of FileData by some file attribute, currently size.
type byFile []FileData

func (x byFile) Len() int { return len(x) }

func (x byFile) Swap(i, j int) { x[i], x[j] = x[j], x[i] }

func (x byFile) Less(i, j int) bool {
	si, sj := x[i].Size(), x[j].Size()
	if si != sj {
		return si < sj
	}
	return x[i].path < x[j].path
}

const (
	gsImageFormat  = "doc-%03d.png"
	gsImagePattern = `doc-(\d+).png$`
)

var gsImageRegex = regexp.MustCompile(gsImagePattern)

// runGhostscript runs Ghostscript on file `pdf` to create file one png file per page in directory
// `outputDir`
func runGhostscript(pdf, outputDir string) error {
	common.Log.Debug("runGhostscript: pdf=%#q outputDir=%#q", pdf, outputDir)
	outputPath := filepath.Join(outputDir, gsImageFormat)
	output := fmt.Sprintf("-sOutputFile=%s", outputPath)

	cmd := exec.Command(
		ghostscriptName(),
		"-dSAFER",
		"-dBATCH",
		"-dNOPAUSE",
		"-r150",
		fmt.Sprintf("-sDEVICE=png16m"),
		"-dTextAlphaBits=1",
		"-dGraphicsAlphaBits=1",
		output,
		pdf)
	common.Log.Debug("runGhostscript: cmd=%#q", cmd.Args)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		common.Log.Error("runGhostscript: Could not process pdf=%q err=%v\nstdout=\n%s\nstderr=\n%s\n",
			pdf, err, stdout, stderr)
	}
	return err
}

// ghostscriptName returns the name of the Ghostscript binary on this OS
func ghostscriptName() string {
	if runtime.GOOS == "windows" {
		return "gswin64c.exe"
	}
	return "gs"
}

// gsMarkedPages returns a list of the (1-offset) page numbers of the marked pages in PDF at `path`
func gsMarkedPages(path, dir string, keep bool) ([]int, error) {
	removeDir(dir)
	err := os.MkdirAll(dir, 0777)
	if err != nil {
		panic(err)
	}

	err = runGhostscript(path, dir)
	if err != nil {
		return nil, err
	}

	return markedDirectoryPages("*.png", dir)
}

// markedDirectoryPages returns a list of the (1-offset) page numbers of the image files that match
// `mask` in  directories `dir` that have any marked pixels.
func markedDirectoryPages(mask, dir string) ([]int, error) {
	pattern := filepath.Join(dir, mask)
	files, err := filepath.Glob(pattern)
	if err != nil {
		common.Log.Error("markedDirectoryPages: Glob failed. pattern=%#q err=%v", pattern, err)
		return nil, err
	}

	markedPagesPredicted := []int{}
	for _, path := range files {
		matches := gsImageRegex.FindStringSubmatch(path)
		if len(matches) == 0 {
			continue
		}
		pageNum, err := strconv.Atoi(matches[1])
		if err != nil {
			panic(err)
			return markedPagesPredicted, err
		}
		isColor, err := isMarkedImage(path)
		if err != nil {
			panic(err)
			return markedPagesPredicted, err
		}
		if isColor {
			markedPagesPredicted = append(markedPagesPredicted, pageNum)
		}
	}
	return markedPagesPredicted, nil
}

// isMarkedImage returns true if image file `path` is marked
func isMarkedImage(path string) (bool, error) {
	img, err := readImage(path)
	if err != nil {
		return false, err
	}
	return imgIsMarked(img), nil
}

// ColorTolerance is the smallest color component that is visible on a typical mid-range color laser printer
// cpts have values in range 0.0-1.0
const colorTolerance = 1.0 / 255.0

// visibleThreshold is the r,g,b values for which a pixel is considered to be visible
// Color components are in range 0-0xFFFF
// We make this 10x the PDF color threshold as guess
const visibleThreshold = float64(0xFFFF) * (1.0 - colorTolerance*1.0)

// imgIsMarked returns true if image `img` has any marked pixels
func imgIsMarked(img image.Image) bool {
	w, h := img.Bounds().Max.X, img.Bounds().Max.Y
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			rr, gg, bb, _ := img.At(x, y).RGBA()
			r, g, b := float64(rr), float64(gg), float64(bb)
			if math.Abs(r) < visibleThreshold && math.Abs(g) < visibleThreshold && math.Abs(b) < visibleThreshold {
				fmt.Printf("$$$$$ %.3f,%.3f,%.3f\n", r, g, b)
				fmt.Printf("$$$** %+v,%+v,%+v\n", rr, gg, bb)
				return true
			}
		}
	}
	return false
}

// readImage reads image file `path` and returns its contents as an Image.
func readImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		common.Log.Error("readImage: Could not open file. path=%#q err=%v", path, err)
		return nil, err
	}
	defer f.Close()

	return png.Decode(f)
}

// makeUniqueDir creates a new directory inside `baseDir`
func makeUniqueDir(baseDir string) string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < 1000; i++ {
		dir := filepath.Join(baseDir, fmt.Sprintf("dir.%03d", i))
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				if err := os.MkdirAll(dir, 0777); err != nil {
					panic(err)
				}
				return dir
			}
		}
		time.Sleep(time.Duration(r.Float64() * float64(time.Second)))
	}
	panic("Cannot create new directory")
}

// removeDir removes directory `dir` and its contents
func removeDir(dir string) error {
	err1 := os.RemoveAll(dir)
	err2 := os.Remove(dir)
	if err1 != nil {
		return err1
	}
	return err2
}

// patternsToPaths returns a list of files matching the patterns in `patternList`
func patternsToPaths(patternList []string) ([]string, error) {
	pathList := []string{}
	for _, pattern := range patternList {
		files, err := filepath.Glob(pattern)
		if err != nil {
			common.Log.Error("patternsToPaths: Glob failed. pattern=%#q err=%v", pattern, err)
			return pathList, err
		}
		for _, path := range files {
			if !regularFile(path) {
				fmt.Fprintf(os.Stderr, "Not a regular file. %#q\n", path)
				continue
			}
			pathList = append(pathList, path)
		}
	}
	return pathList, nil
}

// regularFile returns true if file `path` is a regular file
func regularFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	return fi.Mode().IsRegular()
}

// fileSize returns the size of file `path` in bytes
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	return fi.Size()
}

// sliceDiff returns the elements in a that aren't in b
func sliceDiff(a, b []int) []int {
	mb := map[int]bool{}
	for _, x := range b {
		mb[x] = true
	}
	ab := []int{}
	for _, x := range a {
		if _, ok := mb[x]; !ok {
			ab = append(ab, x)
		}
	}
	return ab
}

type Summary struct {
	NumPages    int
	Width       float64
	Height      float64
	MarkedPages []int
}

func readSummary(b []byte) (a Summary, err error) {
	err = json.Unmarshal(b, &a)
	if err != nil {
		common.Log.Error("readSummary: err=%v text=%s", err, string(b))
		panic(err)
	}
	return
}
