import re
adobe_lines = [

        # "\u00a9 Adobe Systems Incorporated 2008 \u2013 All rights reserved 643",
        # "PDF 32000-1:2008",
        # "Annex",
        # "A",
        # "(informative)",
        # "Operator Summary",
        # "A.1 General",
        # "This annex lists, in alphabetical order, all the operators that may be used in PDF content streams.",
        # "A.2 PDF Content Stream Operators",
        # "Table A.1 lists each operator, its corresponding PostScript language operators (when it is an exact or nearexact",
        # "equivalent of the PDF operator), a description of the operator, and references to the table and page",
        # "where each operator is introduced.",
        # "Table A.1 \u2013",
        # "PDF content stream operators",

        # "Operator PostScript Equivalent Description Table",

        "b closepath, fill, stroke Close, fill, and stroke path using nonzero winding number rule 60",
        "B fill, stroke Fill and stroke path using nonzero winding number rule 60",
        "b* closepath, eofill, stroke Close, fill, and stroke path using even-odd rule 60",
        "B* eofill, stroke Fill and stroke path using even-odd rule 60",
        "BDC (PDF 1.2) Begin marked-content sequence with property list 320",
        "BI Begin inline image object 92",
        "BMC (PDF 1.2) Begin marked-content sequence 320",
        "BT Begin text object 107",
        "BX (PDF 1.1) Begin compatibility section 32",
        "c curveto Append curved segment to path (three control points) 59",
        "cm concat Concatenate matrix to current transformation matrix 57",
        "CS setcolorspace (PDF 1.1) Set color space for stroking operations 74",
        "cs setcolorspace (PDF 1.1) Set color space for nonstroking operations 74",
        "d setdash Set line dash pattern 57",
        "d0 setcharwidth Set glyph width in Type 3 font 113",
        "d1 setcachedevice Set glyph width and bounding box in Type 3 font 113",
        "Do Invoke named XObject 87",
        "DP (PDF 1.2) Define marked-content point with property list 320",
        "EI End inline image object 92",
        "EMC (PDF 1.2) End marked-content sequence 320",

        # "PDF 32000-1:2008",
        # "644 \u00a9 Adobe Systems Incorporated 2008 \u2013 All rights reserved",
        "ET End text object 107",
        "EX (PDF 1.1) End compatibility section 32",
        "f fill Fill path using nonzero winding number rule 60",
        "F fill Fill path using nonzero winding number rule (obsolete) 60",
        "f* eofill Fill path using even-odd rule 60",
        "G setgray Set gray level for stroking operations 74",
        "g setgray Set gray level for nonstroking operations 74",
        "gs (PDF 1.2) Set parameters from graphics state parameter dictionary 57",
        "h closepath Close subpath 59",
        "i setflat Set flatness tolerance 57",
        "ID Begin inline image data 92",
        "j setlinejoin Set line join style 57",
        "J setlinecap Set line cap style 57",
        "K setcmykcolor Set CMYK color for stroking operations 74",
        "k setcmykcolor Set CMYK color for nonstroking operations 74",
        "l lineto Append straight line segment to path 59",
        "m moveto Begin new subpath 59",
        "M setmiterlimit Set miter limit 57",
        "MP (PDF 1.2) Define marked-content point 320",
        "n End path without filling or stroking 60",
        "q gsave Save graphics state 57",
        "Q grestore Restore graphics state 57",
        "re Append rectangle to path 59",
        "RG setrgbcolor Set RGB color for stroking operations 74",
        "rg setrgbcolor Set RGB color for nonstroking operations 74",
        "ri Set color rendering intent 57",
        "s closepath, stroke Close and stroke path 60",
        "S stroke Stroke path 60",
        "SC setcolor (PDF 1.1) Set color for stroking operations 74",
        "sc setcolor (PDF 1.1) Set color for nonstroking operations 74",
        "SCN setcolor (PDF 1.2) Set color for stroking operations (ICCBased and special colour spaces) 74",
        "scn setcolor (PDF 1.2) Set color for nonstroking operations (ICCBased and special colour spaces) 74",

        # "\u00a9 Adobe Systems Incorporated 2008 \u2013 All rights reserved 645",
        # "PDF 32000-1:2008",
        "sh shfill (PDF 1.3) Paint area defined by shading pattern 77",
        "T* Move to start of next text line 108",
        "Tc Set character spacing",
        "Td Move text position 108",
        "TD Move text position and set leading 108",
        "Tf selectfont Set text font and size",
        "Tj show Show text 109",
        "TJ Show text, allowing individual glyph positioning 109",
        "TL Set text leading",
        "Tm Set text matrix and text line matrix 108",
        "Tr Set text rendering mode",
        "Ts Set text rise",
        "Tw Set word spacing",
        "Tz Set horizontal text scaling",
        "v curveto Append curved segment to path (initial point replicated) 59",
        "w setlinewidth Set line width 57",
        "W clip Set clipping path using nonzero winding number rule 61",
        "W* eoclip Set clipping path using even-odd rule 61",
        "y curveto Append curved segment to path (final point replicated) 59",
        "' Move to next line and show text 109",
        "\" Set word and character spacing, move to next line, and show text 109",
]

print('%d lines' % len(adobe_lines))

RE_LINE = re.compile(r'^(.*?)\s+(.*?)(\d+)?$')
# for i, line in enumerate(adobe_lines):
#     m = RE_LINE.search(line)
#     print('%3d: %s' % (i, m.groups()))


print('var markingOperators = map[string]bool{')
for i, line in enumerate(adobe_lines):
    m = RE_LINE.search(line)
    print('\t`%s`: false,  // %3d: %s' % (m.group(1), i, m.group(2)))
print('}')
