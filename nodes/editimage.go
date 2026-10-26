package nodes

// The Edit Image node (FEAT-re138f): resize, crop and addText over the
// workflow's binary store, entirely in pure Go.
//
// Decode and encode are the standard library's PNG, JPEG and GIF codecs; the
// resize is a hand-written bilinear interpolation and the text is drawn with
// a compact bitmap face, because both are what a workflow's overlay and
// thumbnail steps need and neither justifies an imaging dependency. n8n's
// other operations — composite, rotate, blur, border — are refused by name at
// import and at run time rather than half-run.
//
// The parameter keys are n8n's own (`operation`, `width`, `height`,
// `resizeMode`, `top`, `left`, `text`, `fontSize`, `format`, `quality`,
// `binaryPropertyName`).
import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	EditImageExecutorID = "core.editImage"
	EditImageNodeType   = "kilasflow.editImage"
)

// The Edit Image operations this node runs, under n8n's values.
const (
	EditImageResize  = "resize"
	EditImageCrop    = "crop"
	EditImageAddText = "addText"
)

func editImageNode() node.Definition {
	return node.Definition{
		Type:        EditImageNodeType,
		Version:     workflow.V(1),
		DisplayName: "Edit Image",
		Description: "Resizes, crops or writes text onto an image from the workflow's binary data, using pure-Go codecs: PNG, JPEG and GIF in and out.",
		Category:    "Files",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "editImage"},
		IconLight:   editImageArtwork(),
		IconColor:   "#db2777",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: EditImageResize,
				Options: []node.PropertyOption{
					{Label: "Resize", Value: EditImageResize},
					{Label: "Crop", Value: EditImageCrop},
					{Label: "Add Text", Value: EditImageAddText},
				},
			},
			{
				Key: "width", Label: "Width", Kind: node.PropertyNumber, Default: 300,
				Description: "The target width in pixels.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: EditImageResize},
					{Key: "operation", Equals: EditImageCrop}},
			},
			{
				Key: "height", Label: "Height", Kind: node.PropertyNumber, Default: 300,
				Description: "The target height in pixels.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: EditImageResize},
					{Key: "operation", Equals: EditImageCrop}},
			},
			{
				Key: "resizeMode", Label: "Resize Mode", Kind: node.PropertyOptions, Default: "exact",
				Options: []node.PropertyOption{
					{Label: "Exact Dimensions", Value: "exact"},
					{Label: "Keep Aspect Ratio", Value: "keepAspect"},
				},
				Description: "Exact stretches to the width and height; Keep Aspect Ratio fits inside them.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: EditImageResize}},
			},
			{
				Key: "top", Label: "Top", Kind: node.PropertyNumber, Default: 0,
				Description: "The crop's or the text's top edge, in pixels from the image's top.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: EditImageCrop},
					{Key: "operation", Equals: EditImageAddText}},
			},
			{
				Key: "left", Label: "Left", Kind: node.PropertyNumber, Default: 0,
				Description: "The crop's or the text's left edge, in pixels from the image's left.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: EditImageCrop},
					{Key: "operation", Equals: EditImageAddText}},
			},
			{
				Key: "text", Label: "Text", Kind: node.PropertyString,
				Description: "The text written onto the image. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: EditImageAddText}},
			},
			{
				Key: "fontSize", Label: "Font Size", Kind: node.PropertyNumber, Default: 24,
				Description: "The text's height in pixels.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: EditImageAddText}},
			},
			{
				Key: "textColor", Label: "Text Color", Kind: node.PropertyString, Default: "#ffffff",
				Description: "The text's colour, as a hex value like #ffffff. A dark outline is drawn behind it so it stays readable on any background.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: EditImageAddText}},
			},
			{
				Key: "binaryPropertyName", Label: "Input Binary Field", Kind: node.PropertyString, Default: "data",
				Description: "The binary field the image is read from; the edited image is written back to it.",
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{
						Key: "format", Label: "Output Format", Kind: node.PropertyOptions, Default: "png",
						Options: []node.PropertyOption{
							{Label: "PNG", Value: "png"},
							{Label: "JPEG", Value: "jpeg"},
						},
						Description: "The written file's format; the default keeps PNG whatever the input was. WebP needs an imaging library this node does not carry.",
					},
					{
						Key: "quality", Label: "JPEG Quality", Kind: node.PropertyNumber, Default: 95,
						TypeOptions: &node.TypeOptions{MinValue: numberBound(1), MaxValue: numberBound(100)},
					},
					{
						Key: "fileName", Label: "File Name", Kind: node.PropertyString,
						Description: "The edited file's name. Defaults to the input's name with the output format's extension.",
					},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     EditImageExecutorID,
		Validate:       validateEditImage,
	}
}

func validateEditImage(n workflow.Node) error {
	switch operation := statementText(n.Parameters, "operation"); operation {
	case "", EditImageResize, EditImageCrop, EditImageAddText:
	default:
		return fmt.Errorf("operation %q is not one this node runs; it resizes, crops or adds text", operation)
	}
	switch format := statementText(mapValue(n.Parameters["options"]), "format"); format {
	case "", "png", "jpeg":
	default:
		return fmt.Errorf("format %q is not one this node writes; it writes png or jpeg", format)
	}
	return nil
}

func executeEditImage(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: binary storage is not configured on this install", ir.Name)
	}
	items := input["main"]
	if len(items) == 0 {
		return workflow.NodeOutput{[]workflow.Item{}}, nil
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		key := strings.TrimSpace(textValue(parameters["binaryPropertyName"], "data"))
		if key == "" {
			key = "data"
		}
		reference, found := firstBinary(item, key)
		if !found {
			return nil, fmt.Errorf("node %q: item %d has no binary property %q", ir.Name, index+1, key)
		}
		body, _, err := request.Binaries.Get(reference.ID)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(body, maxFileFormatBytes))
		_ = body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("node %q: item %d could not be read: %w", ir.Name, index+1, readErr)
		}
		source, _, err := image.Decode(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: the binary is not a PNG, JPEG or GIF image: %w", ir.Name, index+1, err)
		}
		operation := textValue(parameters["operation"], EditImageResize)
		var edited image.Image
		switch operation {
		case EditImageResize:
			width := positiveInt(numberValue(parameters["width"]))
			height := positiveInt(numberValue(parameters["height"]))
			if width == 0 || height == 0 {
				return nil, fmt.Errorf("node %q: item %d: resize needs a width and a height above zero", ir.Name, index+1)
			}
			edited = resizeImage(source, width, height, textValue(parameters["resizeMode"], "exact") == "keepAspect")
		case EditImageCrop:
			edited = cropImage(source,
				positiveInt(numberValue(parameters["width"])),
				positiveInt(numberValue(parameters["height"])),
				int(numberValue(parameters["top"])), int(numberValue(parameters["left"])))
		case EditImageAddText:
			text := textValue(parameters["text"], "")
			if strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("node %q: item %d: addText needs its text", ir.Name, index+1)
			}
			size := positiveInt(numberValue(parameters["fontSize"]))
			if size == 0 {
				size = 24
			}
			edited = drawBitmapText(source, text, size, textValue(parameters["textColor"], "#ffffff"),
				int(numberValue(parameters["top"])), int(numberValue(parameters["left"])))
		default:
			return nil, fmt.Errorf("node %q: operation %q has no runner here", ir.Name, operation)
		}
		options := mapValue(parameters["options"])
		format := textValue(options["format"], "png")
		if format == "" {
			format = "png"
		}
		var buffer bytes.Buffer
		switch format {
		case "jpeg":
			if err := jpeg.Encode(&buffer, edited, &jpeg.Options{Quality: int(numberValue(options["quality"]))}); err != nil {
				return nil, fmt.Errorf("node %q: item %d: the image could not be encoded: %w", ir.Name, index+1, err)
			}
		default:
			if err := png.Encode(&buffer, edited); err != nil {
				return nil, fmt.Errorf("node %q: item %d: the image could not be encoded: %w", ir.Name, index+1, err)
			}
		}
		name := strings.TrimSpace(textValue(options["fileName"], ""))
		if name == "" {
			name = strings.TrimSuffix(reference.FileName, "."+extensionOf(reference.FileName)) + "." + format
			if reference.FileName == "" {
				name = "image." + format
			}
		}
		media := "image/" + format
		if format == "jpeg" {
			media = "image/jpeg"
		}
		stored, err := request.Binaries.Put(name, media, bytes.NewReader(buffer.Bytes()))
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		fields, err := cloneItemJSON(item.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d is not JSON-serialisable: %w", ir.Name, index+1, err)
		}
		binary := map[string]workflow.BinaryRef{}
		for field, value := range item.Binary {
			binary[field] = value
		}
		binary[key] = stored
		output = append(output, workflow.Item{JSON: fields, Binary: binary, Paired: item.Paired})
	}
	return workflow.NodeOutput{output}, nil
}

func extensionOf(name string) string {
	if index := strings.LastIndexByte(name, '.'); index >= 0 {
		return name[index+1:]
	}
	return ""
}

// resizeImage scales the image with bilinear interpolation. Exact stretches
// to the requested dimensions; keepAspectRatio fits inside them, which may
// leave one dimension under target — the image's own shape is the truth and
// a padded letterbox would invent pixels the author never asked for.
func resizeImage(source image.Image, width, height int, keepAspect bool) image.Image {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	if keepAspect && sourceWidth > 0 && sourceHeight > 0 {
		ratio := float64(width) / float64(height)
		if float64(sourceWidth)/float64(sourceHeight) > ratio {
			height = max(1, int(float64(width)*float64(sourceHeight)/float64(sourceWidth)))
		} else {
			width = max(1, int(float64(height)*float64(sourceWidth)/float64(sourceHeight)))
		}
	}
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			destination.Set(x, y, bilinearSample(source,
				(float64(x)+0.5)*float64(sourceWidth)/float64(width)-0.5,
				(float64(y)+0.5)*float64(sourceHeight)/float64(height)-0.5))
		}
	}
	return destination
}

// bilinearSample reads one interpolated pixel, clamping at the edges.
func bilinearSample(source image.Image, x, y float64) color.Color {
	bounds := source.Bounds()
	clamp := func(value, low, high float64) float64 {
		if value < low {
			return low
		}
		if value > high {
			return high
		}
		return value
	}
	x0, y0 := int(clamp(x, 0, float64(bounds.Dx()-1))), int(clamp(y, 0, float64(bounds.Dy()-1)))
	x1, y1 := min(x0+1, bounds.Dx()-1), min(y0+1, bounds.Dy()-1)
	fx, fy := clamp(x-float64(x0), 0, 1), clamp(y-float64(y0), 0, 1)
	at := func(px, py int) color.RGBA64 {
		r, g, b, a := source.At(bounds.Min.X+px, bounds.Min.Y+py).RGBA()
		return color.RGBA64{R: uint16(r), G: uint16(g), B: uint16(b), A: uint16(a)}
	}
	topLeft, topRight := at(x0, y0), at(x1, y0)
	bottomLeft, bottomRight := at(x0, y1), at(x1, y1)
	mix := func(a, b uint16, weight float64) uint16 {
		return uint16(float64(a)*(1-weight) + float64(b)*weight)
	}
	return color.RGBA64{
		R: mix(mix(topLeft.R, topRight.R, fx), mix(bottomLeft.R, bottomRight.R, fx), fy),
		G: mix(mix(topLeft.G, topRight.G, fx), mix(bottomLeft.G, bottomRight.G, fx), fy),
		B: mix(mix(topLeft.B, topRight.B, fx), mix(bottomLeft.B, bottomRight.B, fx), fy),
		A: mix(mix(topLeft.A, topRight.A, fx), mix(bottomLeft.A, bottomRight.A, fx), fy),
	}
}

// cropImage cuts the rectangle out, clamped to the image: a crop that runs
// past an edge takes what is there rather than failing the run, which is what
// n8n's sharp-backed crop does with its default fit.
func cropImage(source image.Image, width, height, top, left int) image.Image {
	bounds := source.Bounds()
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	if left > bounds.Dx() {
		left = bounds.Dx()
	}
	if top > bounds.Dy() {
		top = bounds.Dy()
	}
	if width <= 0 || left+width > bounds.Dx() {
		width = bounds.Dx() - left
	}
	if height <= 0 || top+height > bounds.Dy() {
		height = bounds.Dy() - top
	}
	rectangle := image.Rect(0, 0, width, height)
	cropped := image.NewRGBA(rectangle)
	draw.Draw(cropped, rectangle, source, image.Point{X: bounds.Min.X + left, Y: bounds.Min.Y + top}, draw.Src)
	return cropped
}

// drawBitmapText writes the text onto the image with a compact 5x7 bitmap
// face scaled to the requested size, top-left at (left, top), with a
// one-pixel dark outline so the glyphs stay readable on a light background.
// The face is this file's own, which is the honest pure-Go answer: no font
// file to load, no imaging dependency to carry.
func drawBitmapText(source image.Image, text string, size int, hexColor string, top, left int) image.Image {
	bounds := source.Bounds()
	destination := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(destination, destination.Bounds(), source, bounds.Min, draw.Src)
	foreground, err := parseHexColor(hexColor)
	if err != nil {
		foreground = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	glyphHeight := size
	glyphWidth := max(1, glyphHeight*5/7)
	scale := max(1, glyphHeight/7)
	x := left
	for _, character := range text {
		if character == '\n' {
			x = left
			top += glyphHeight + scale
			continue
		}
		glyph, _ := bitmapGlyph(byte(character))
		for row := 0; row < 7; row++ {
			for column := 0; column < 5; column++ {
				if glyph[row]&(1<<(4-column)) == 0 {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						// The outline: every pixel at one step outside the
						// glyph becomes near-black before the face is drawn
						// over it.
						for _, offset := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
							destination.Set(x+column*scale+dx+offset[0], top+row*scale+dy+offset[1], color.RGBA{R: 0, G: 0, B: 0, A: 255})
						}
						destination.Set(x+column*scale+dx, top+row*scale+dy, foreground)
					}
				}
			}
		}
		x += glyphWidth + scale
	}
	return destination
}

func parseHexColor(text string) (color.RGBA, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "#") {
		return color.RGBA{}, fmt.Errorf("the colour %q is not a hex value", text)
	}
	digits := strings.TrimPrefix(text, "#")
	if len(digits) == 3 {
		digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
	}
	if len(digits) != 6 {
		return color.RGBA{}, fmt.Errorf("the colour %q is not a hex value", text)
	}
	value := 0
	for _, digit := range digits {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= int(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= int(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= int(digit-'A') + 10
		default:
			return color.RGBA{}, fmt.Errorf("the colour %q is not a hex value", text)
		}
	}
	return color.RGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255}, nil
}

// bitmapGlyph returns one 5x7 face: seven rows of five bits, the most
// significant bit the leftmost pixel. The ASCII printable set only — a
// character outside it draws a hollow box, which is what a bitmap face can
// honestly say.
func bitmapGlyph(character byte) ([7]byte, bool) {
	if face, known := bitmapFace[character]; known {
		return face, true
	}
	if character == ' ' {
		return [7]byte{0, 0, 0, 0, 0, 0, 0}, true
	}
	return [7]byte{0x0e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e}, false
}

// bitmapFace is the classic public-domain 5x7 ASCII face: every glyph five
// pixels wide and seven tall, bits set where the mark is drawn.
var bitmapFace = map[byte][7]byte{
	'A':  {0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'B':  {0b11110, 0b10001, 0b10001, 0b11110, 0b10001, 0b10001, 0b11110},
	'C':  {0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110},
	'D':  {0b11100, 0b10010, 0b10001, 0b10001, 0b10001, 0b10010, 0b11100},
	'E':  {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111},
	'F':  {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b10000},
	'G':  {0b01110, 0b10001, 0b10000, 0b10111, 0b10001, 0b10001, 0b01111},
	'H':  {0b10001, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'I':  {0b01110, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'J':  {0b00111, 0b00010, 0b00010, 0b00010, 0b00010, 0b10010, 0b01100},
	'K':  {0b10001, 0b10010, 0b10100, 0b11000, 0b10100, 0b10010, 0b10001},
	'L':  {0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b11111},
	'M':  {0b10001, 0b11011, 0b10101, 0b10101, 0b10001, 0b10001, 0b10001},
	'N':  {0b10001, 0b10001, 0b11001, 0b10101, 0b10011, 0b10001, 0b10001},
	'O':  {0b01110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'P':  {0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000},
	'Q':  {0b01110, 0b10001, 0b10001, 0b10001, 0b10101, 0b10010, 0b01101},
	'R':  {0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001},
	'S':  {0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110},
	'T':  {0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100},
	'U':  {0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'V':  {0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01010, 0b00100},
	'W':  {0b10001, 0b10001, 0b10001, 0b10101, 0b10101, 0b11011, 0b10001},
	'X':  {0b10001, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001, 0b10001},
	'Y':  {0b10001, 0b10001, 0b01010, 0b00100, 0b00100, 0b00100, 0b00100},
	'Z':  {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b10000, 0b11111},
	'a':  {0b00000, 0b00000, 0b01110, 0b00001, 0b01111, 0b10001, 0b01111},
	'b':  {0b10000, 0b10000, 0b11110, 0b10001, 0b10001, 0b10001, 0b11110},
	'c':  {0b00000, 0b00000, 0b01110, 0b10001, 0b10000, 0b10001, 0b01110},
	'd':  {0b00001, 0b00001, 0b01111, 0b10001, 0b10001, 0b10001, 0b01111},
	'e':  {0b00000, 0b00000, 0b01110, 0b10001, 0b11111, 0b10000, 0b01110},
	'f':  {0b00110, 0b01001, 0b01000, 0b11100, 0b01000, 0b01000, 0b01000},
	'g':  {0b00000, 0b01111, 0b10001, 0b10001, 0b01111, 0b00001, 0b01110},
	'h':  {0b10000, 0b10000, 0b11110, 0b10001, 0b10001, 0b10001, 0b10001},
	'i':  {0b00100, 0b00000, 0b01100, 0b00100, 0b00100, 0b00100, 0b01110},
	'j':  {0b00010, 0b00000, 0b00110, 0b00010, 0b00010, 0b10010, 0b01100},
	'k':  {0b10000, 0b10000, 0b10010, 0b10100, 0b11000, 0b10100, 0b10010},
	'l':  {0b01100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'm':  {0b00000, 0b00000, 0b11010, 0b10101, 0b10101, 0b10101, 0b10101},
	'n':  {0b00000, 0b00000, 0b11110, 0b10001, 0b10001, 0b10001, 0b10001},
	'o':  {0b00000, 0b00000, 0b01110, 0b10001, 0b10001, 0b10001, 0b01110},
	'p':  {0b00000, 0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000},
	'q':  {0b00000, 0b01111, 0b10001, 0b10001, 0b01111, 0b00001, 0b00001},
	'r':  {0b00000, 0b00000, 0b01111, 0b10000, 0b10000, 0b10000, 0b10000},
	's':  {0b00000, 0b00000, 0b01111, 0b10000, 0b01110, 0b00001, 0b11110},
	't':  {0b01000, 0b01000, 0b11100, 0b01000, 0b01000, 0b01001, 0b00110},
	'u':  {0b00000, 0b00000, 0b10001, 0b10001, 0b10001, 0b10011, 0b01101},
	'v':  {0b00000, 0b00000, 0b10001, 0b10001, 0b10001, 0b01010, 0b00100},
	'w':  {0b00000, 0b00000, 0b10101, 0b10101, 0b10101, 0b10101, 0b01010},
	'x':  {0b00000, 0b00000, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001},
	'y':  {0b00000, 0b10001, 0b10001, 0b01111, 0b00001, 0b10001, 0b01110},
	'z':  {0b00000, 0b00000, 0b11111, 0b00010, 0b00100, 0b01000, 0b11111},
	'0':  {0b01110, 0b10001, 0b10011, 0b10101, 0b11001, 0b10001, 0b01110},
	'1':  {0b00100, 0b01100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'2':  {0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0b01000, 0b11111},
	'3':  {0b11111, 0b00010, 0b00100, 0b00010, 0b00001, 0b10001, 0b01110},
	'4':  {0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010},
	'5':  {0b11111, 0b10000, 0b11110, 0b00001, 0b00001, 0b10001, 0b01110},
	'6':  {0b00110, 0b01000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110},
	'7':  {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000},
	'8':  {0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110},
	'9':  {0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00010, 0b01100},
	'.':  {0b00000, 0b00000, 0b00000, 0b00000, 0b00000, 0b01100, 0b01100},
	',':  {0b00000, 0b00000, 0b00000, 0b00000, 0b00110, 0b00110, 0b00100},
	'!':  {0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00000, 0b00100},
	'?':  {0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0b00000, 0b00100},
	'-':  {0b00000, 0b00000, 0b00000, 0b11111, 0b00000, 0b00000, 0b00000},
	'_':  {0b00000, 0b00000, 0b00000, 0b00000, 0b00000, 0b00000, 0b11111},
	'+':  {0b00000, 0b00100, 0b00100, 0b11111, 0b00100, 0b00100, 0b00000},
	'=':  {0b00000, 0b00000, 0b11111, 0b00000, 0b11111, 0b00000, 0b00000},
	'/':  {0b00001, 0b00010, 0b00010, 0b00100, 0b01000, 0b01000, 0b10000},
	'(':  {0b00010, 0b00100, 0b01000, 0b01000, 0b01000, 0b00100, 0b00010},
	')':  {0b01000, 0b00100, 0b00010, 0b00010, 0b00010, 0b00100, 0b01000},
	':':  {0b00000, 0b01100, 0b01100, 0b00000, 0b01100, 0b01100, 0b00000},
	';':  {0b00000, 0b01100, 0b01100, 0b00000, 0b00110, 0b00110, 0b00100},
	'\'': {0b00100, 0b00100, 0b00000, 0b00000, 0b00000, 0b00000, 0b00000},
	'"':  {0b01010, 0b01010, 0b00000, 0b00000, 0b00000, 0b00000, 0b00000},
	'*':  {0b00000, 0b00100, 0b10101, 0b01110, 0b10101, 0b00100, 0b00000},
	'#':  {0b01010, 0b11111, 0b01010, 0b01010, 0b11111, 0b01010, 0b00000},
}

// editImageArtwork is the node's own monochrome mark: a framed picture with
// a pencil over it, in the editor's stroke idiom.
func editImageArtwork() *node.IconAsset {
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			`<rect x="3" y="3" width="14" height="14" rx="2"/><circle cx="8" cy="8" r="1.5"/><path d="m3 13 4-4 5 5"/><path d="m17 15 4-4-2-2-4 4v2z"/>` +
			`</svg>`),
	}
}
