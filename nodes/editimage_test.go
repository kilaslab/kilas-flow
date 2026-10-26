package nodes_test

// FEAT-re138f: Edit Image resizes, crops and writes text over pure-Go codecs.
// The tests decode what the node wrote and point at pixels, because an image
// node's contract is its pixels.

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// editedImage reads one run's edited image back from its binary store.
func editedImage(t *testing.T, output workflow.NodeOutput, store engine.BinaryStore, key string) image.Image {
	t.Helper()
	item := output[0][0]
	reference, found := item.Binary[key]
	if !found {
		t.Fatalf("item carries no binary %q (has %v)", key, item.Binary)
	}
	body, _, err := store.Get(reference.ID)
	if err != nil {
		t.Fatalf("read the edited image: %v", err)
	}
	defer body.Close()
	decoded, _, err := image.Decode(body)
	if err != nil {
		t.Fatalf("decode the edited image: %v", err)
	}
	return decoded
}

func TestEditImageResizesToExactDimensions(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	seed := binaryItem(t, store, "data", "square.png", "image/png", redPNG(t, 10), nil)
	output := executeBinaryNode(t, nodes.EditImageNodeType, map[string]any{
		"operation": "resize", "width": 20, "height": 20, "resizeMode": "exact",
	}, []workflow.Item{seed}, store)
	edited := editedImage(t, output, store, "data")
	if edited.Bounds().Dx() != 20 || edited.Bounds().Dy() != 20 {
		t.Fatalf("bounds = %v, want 20x20", edited.Bounds())
	}
	// The middle of a resized red square is still red; alpha survives.
	red, _, _, alpha := edited.At(10, 10).RGBA()
	if red < 60000 || alpha < 60000 {
		t.Fatalf("centre pixel = %d/%d, want the red it was resized from", red, alpha)
	}
}

func TestEditImageKeepsTheAspectWhenAsked(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	seed := binaryItem(t, store, "data", "square.png", "image/png", redPNG(t, 10), nil)
	output := executeBinaryNode(t, nodes.EditImageNodeType, map[string]any{
		"operation": "resize", "width": 100, "height": 50, "resizeMode": "keepAspect",
	}, []workflow.Item{seed}, store)
	edited := editedImage(t, output, store, "data")
	if edited.Bounds().Dx() != 50 || edited.Bounds().Dy() != 50 {
		t.Fatalf("bounds = %v, want 50x50: the square fits inside 100x50", edited.Bounds())
	}
}

func TestEditImageCropsARectangleOut(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	seed := binaryItem(t, store, "data", "square.png", "image/png", redPNG(t, 10), nil)
	output := executeBinaryNode(t, nodes.EditImageNodeType, map[string]any{
		"operation": "crop", "width": 4, "height": 6, "top": 2, "left": 3,
	}, []workflow.Item{seed}, store)
	if edited := editedImage(t, output, store, "data"); edited.Bounds().Dx() != 4 || edited.Bounds().Dy() != 6 {
		t.Fatalf("bounds = %v, want the 4x6 crop", edited.Bounds())
	}
	// A crop past the edge clamps to what is there rather than failing.
	clamped := executeBinaryNode(t, nodes.EditImageNodeType, map[string]any{
		"operation": "crop", "width": 50, "height": 50, "top": 8, "left": 8,
	}, []workflow.Item{seed}, store)
	if cropped := editedImage(t, clamped, store, "data"); cropped.Bounds().Dx() != 2 || cropped.Bounds().Dy() != 2 {
		t.Fatalf("clamped bounds = %v, want the 2x2 that was left", cropped.Bounds())
	}
}

func TestEditImageWritesTextOntoThePicture(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	seed := binaryItem(t, store, "data", "square.png", "image/png", redPNG(t, 40), nil)
	output := executeBinaryNode(t, nodes.EditImageNodeType, map[string]any{
		"operation": "addText", "text": "OK", "fontSize": 14, "textColor": "#ffffff", "top": 4, "left": 4,
	}, []workflow.Item{seed}, store)
	edited := editedImage(t, output, store, "data")
	// Some pixel in the text's first rows is the white the face drew, and
	// some pixel beside the glyph is the black outline.
	white, black := false, false
	for y := 4; y < 24 && !(white && black); y++ {
		for x := 4; x < 24; x++ {
			red, green, blue, _ := edited.At(x, y).RGBA()
			if red > 60000 && green > 60000 && blue > 60000 {
				white = true
			}
			if red < 20000 && green < 20000 && blue < 20000 {
				black = true
			}
		}
	}
	if !white || !black {
		t.Fatalf("text pixels: white=%v black=%v, want the drawn face and its outline", white, black)
	}
}

func TestEditImageWritesJpegWhenAsked(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	seed := binaryItem(t, store, "data", "square.png", "image/png", redPNG(t, 8), nil)
	output := executeBinaryNode(t, nodes.EditImageNodeType, map[string]any{
		"operation": "resize", "width": 8, "height": 8,
		"options": map[string]any{"format": "jpeg", "quality": 90},
	}, []workflow.Item{seed}, store)
	if reference := output[0][0].Binary["data"]; !strings.HasSuffix(reference.FileName, ".jpeg") {
		t.Fatalf("file name = %q, want the jpeg extension", reference.FileName)
	}
	if _, isJPEG := editedImage(t, output, store, "data").(*image.YCbCr); !isJPEG {
		t.Fatalf("decoded = %T, want a JPEG-decoded image", editedImage(t, output, store, "data"))
	}
}

func TestEditImageRefusesAFormatItCannotWrite(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodes.EditImageNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.EditImageNodeType)
	}
	err := definition.Validate(workflow.Node{Parameters: map[string]any{
		"operation": "resize", "width": 8.0, "height": 8.0,
		"options": map[string]any{"format": "webp"},
	}})
	if err == nil || !strings.Contains(err.Error(), "png or jpeg") {
		t.Fatalf("validate error = %v, want the webp refusal", err)
	}
}
