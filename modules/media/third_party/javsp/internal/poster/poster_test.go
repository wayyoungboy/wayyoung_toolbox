package poster

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessCropsWideFanartAndAddsLabel(t *testing.T) {
	root := t.TempDir()
	fanart := filepath.Join(root, "fanart.png")
	out := filepath.Join(root, "poster.png")
	mark := filepath.Join(root, "mark.png")
	writeSolidPNG(t, fanart, image.Rect(0, 0, 400, 200), color.RGBA{R: 10, G: 20, B: 30, A: 255})
	writeSolidPNG(t, mark, image.Rect(0, 0, 10, 10), color.RGBA{R: 200, G: 0, B: 0, A: 255})

	err := Process(fanart, out, Options{
		AddLabel:     true,
		UncMarkPath:  mark,
		UncensoredUC: true,
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	file, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if bounds := img.Bounds(); bounds.Dx() != 140 || bounds.Dy() != 200 {
		t.Fatalf("bounds = %v, want 140x200", bounds)
	}
	if got := color.RGBAModel.Convert(img.At(1, 191)).(color.RGBA); got.R != 200 {
		t.Fatalf("label pixel = %#v, want red mark", got)
	}
}

func TestProcessUsesFaceDetectorCrop(t *testing.T) {
	root := t.TempDir()
	fanart := filepath.Join(root, "fanart.png")
	out := filepath.Join(root, "poster.png")
	writeGradientPNG(t, fanart, image.Rect(0, 0, 300, 200))

	err := Process(fanart, out, Options{
		FaceDetector: fakeFaceDetector{face: image.Rect(40, 20, 80, 80)},
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	file, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if bounds := img.Bounds(); bounds.Dx() != 140 || bounds.Dy() != 200 {
		t.Fatalf("bounds = %v, want 140x200", bounds)
	}
	if got := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA); got.R != 0 {
		t.Fatalf("left pixel = %#v, want crop starting near face", got)
	}
}

func TestProcessFallsBackWhenFaceDetectorFails(t *testing.T) {
	root := t.TempDir()
	fanart := filepath.Join(root, "fanart.png")
	out := filepath.Join(root, "poster.png")
	writeGradientPNG(t, fanart, image.Rect(0, 0, 300, 200))

	err := Process(fanart, out, Options{
		FaceDetector: fakeFaceDetector{err: errors.New("no detector")},
	})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	file, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA); got.R != 160 {
		t.Fatalf("left pixel = %#v, want default right crop", got)
	}
}

func TestPosterSourceContainsNoSubprocessDetector(t *testing.T) {
	source, err := os.ReadFile("poster.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, parts := range [][2]string{
		{"os/", "exec"},
		{"exec.", "Command"},
		{"python", "3"},
		{"slimeface ", "import"},
		{"Pill", "ow"},
	} {
		forbidden := parts[0] + parts[1]
		if strings.Contains(string(source), forbidden) {
			t.Errorf("poster.go contains forbidden runtime dependency %q", forbidden)
		}
	}
}

func TestPigoDetectorSelectsHighestScoreFace(t *testing.T) {
	root := t.TempDir()
	cascadePath := filepath.Join(root, "facefinder")
	if err := os.WriteFile(cascadePath, syntheticCascade(100, 0.01), 0o644); err != nil {
		t.Fatal(err)
	}
	detector, err := NewPigoDetector(cascadePath)
	if err != nil {
		t.Fatalf("NewPigoDetector returned error: %v", err)
	}

	img := image.NewGray(image.Rect(0, 0, 120, 40))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			value := x * 4
			if x >= 60 {
				value = 255 - (x-60)*4
			}
			if value < 0 {
				value = 0
			}
			img.SetGray(x, y, color.Gray{Y: uint8(value)})
		}
	}

	face, err := detector.DetectFace("fixture.png", img)
	if err != nil {
		t.Fatalf("DetectFace returned error: %v", err)
	}
	if face.Empty() {
		t.Fatal("DetectFace returned no face")
	}
	if center := face.Min.X + face.Dx()/2; center <= 60 {
		t.Fatalf("selected face center = %d, want highest-score face in right half; face=%v", center, face)
	}
}

func TestPigoDetectorReturnsNoFaceWithoutPositiveDetection(t *testing.T) {
	root := t.TempDir()
	cascadePath := filepath.Join(root, "facefinder")
	if err := os.WriteFile(cascadePath, syntheticCascade(-1, -1), 0o644); err != nil {
		t.Fatal(err)
	}
	detector, err := NewPigoDetector(cascadePath)
	if err != nil {
		t.Fatalf("NewPigoDetector returned error: %v", err)
	}

	face, err := detector.DetectFace("fixture.png", image.NewGray(image.Rect(0, 0, 80, 40)))
	if err != nil {
		t.Fatalf("DetectFace returned error: %v", err)
	}
	if !face.Empty() {
		t.Fatalf("DetectFace = %v, want empty rectangle", face)
	}
}

func TestProcessFallsBackWhenPigoDetectorFindsNoFace(t *testing.T) {
	root := t.TempDir()
	cascadePath := filepath.Join(root, "facefinder")
	if err := os.WriteFile(cascadePath, syntheticCascade(-1, -1), 0o644); err != nil {
		t.Fatal(err)
	}
	detector, err := NewPigoDetector(cascadePath)
	if err != nil {
		t.Fatalf("NewPigoDetector returned error: %v", err)
	}
	fanart := filepath.Join(root, "fanart.png")
	output := filepath.Join(root, "poster.png")
	writeGradientPNG(t, fanart, image.Rect(0, 0, 300, 200))

	if err := Process(fanart, output, Options{FaceDetector: detector}); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if got := color.RGBAModel.Convert(decodePNG(t, output).At(0, 0)).(color.RGBA); got.R != 160 {
		t.Fatalf("left pixel = %#v, want default right crop", got)
	}
}

func TestNewPigoDetectorRejectsInvalidCascade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facefinder")
	if err := os.WriteFile(path, []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if detector, err := NewPigoDetector(path); err == nil {
		t.Fatalf("NewPigoDetector returned detector %#v for invalid cascade", detector)
	}
}

func TestValidateAcceptsSupportedFormats(t *testing.T) {
	root := t.TempDir()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(20 * x), G: uint8(30 * y), B: 80, A: 255})
		}
	}

	formats := map[string]func(*os.File) error{
		"jpeg": func(file *os.File) error { return jpeg.Encode(file, img, nil) },
		"png":  func(file *os.File) error { return png.Encode(file, img) },
		"gif":  func(file *os.File) error { return gif.Encode(file, img, nil) },
	}
	for name, encode := range formats {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name+".image")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := encode(file); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := Validate(path); err != nil {
				t.Fatalf("Validate returned error: %v", err)
			}
		})
	}

	t.Run("webp", func(t *testing.T) {
		path := filepath.Join(root, "webp.image")
		data, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Validate(path); err != nil {
			t.Fatalf("Validate returned error: %v", err)
		}
	})
}

func TestNormalizeAppliesJPEGEXIFOrientation(t *testing.T) {
	cases := []struct {
		orientation byte
		wantBounds  image.Rectangle
		wantCorner  color.NRGBA
	}{
		{orientation: 3, wantBounds: image.Rect(0, 0, 40, 60), wantCorner: color.NRGBA{R: 255, G: 255, A: 255}},
		{orientation: 6, wantBounds: image.Rect(0, 0, 60, 40), wantCorner: color.NRGBA{B: 255, A: 255}},
		{orientation: 8, wantBounds: image.Rect(0, 0, 60, 40), wantCorner: color.NRGBA{G: 255, A: 255}},
	}
	for _, tc := range cases {
		t.Run(string(rune('0'+tc.orientation)), func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source.jpg")
			destination := filepath.Join(root, "normalized.png")
			writeOrientedJPEG(t, source, tc.orientation)

			if err := Normalize(source, destination); err != nil {
				t.Fatalf("Normalize returned error: %v", err)
			}
			img := decodePNG(t, destination)
			if img.Bounds() != tc.wantBounds {
				t.Fatalf("bounds = %v, want %v", img.Bounds(), tc.wantBounds)
			}
			got := color.NRGBAModel.Convert(img.At(5, 5)).(color.NRGBA)
			if colorDistance(got, tc.wantCorner) > 80 {
				t.Fatalf("top-left pixel = %#v, want near %#v", got, tc.wantCorner)
			}
		})
	}
}

func TestProcessCompositesAlphaMark(t *testing.T) {
	root := t.TempDir()
	fanart := filepath.Join(root, "fanart.png")
	mark := filepath.Join(root, "mark.png")
	out := filepath.Join(root, "poster.png")
	writeSolidColorPNG(t, fanart, image.Rect(0, 0, 40, 40), color.NRGBA{R: 20, G: 40, B: 60, A: 255})
	writeSolidColorPNG(t, mark, image.Rect(0, 0, 10, 10), color.NRGBA{R: 220, A: 128})

	if err := Process(fanart, out, Options{AddLabel: true, UncensoredUC: true, UncMarkPath: mark}); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	got := color.NRGBAModel.Convert(decodePNG(t, out).At(2, 32)).(color.NRGBA)
	if got.R <= 20 || got.R >= 220 || got.B == 0 || got.B >= 60 {
		t.Fatalf("composited pixel = %#v, want translucent red over original color", got)
	}
}

func TestValidateAndNormalizeRejectInvalidImagesWithoutOutput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "truncated.png")
	destination := filepath.Join(root, "normalized.png")
	if err := os.WriteFile(source, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(source); err == nil {
		t.Fatal("Validate accepted a truncated PNG")
	}
	if err := Normalize(source, destination); err == nil {
		t.Fatal("Normalize accepted a truncated PNG")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failed Normalize: %v", err)
	}
}

func TestNormalizeEncodesBytesMatchingDestinationExtension(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.gif")
	file, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(file, image.NewNRGBA(image.Rect(0, 0, 5, 4)), nil); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	for extension, wantFormat := range map[string]string{".jpg": "jpeg", ".png": "png"} {
		destination := filepath.Join(root, "normalized"+extension)
		if err := Normalize(source, destination); err != nil {
			t.Fatalf("Normalize(%s) returned error: %v", extension, err)
		}
		file, err := os.Open(destination)
		if err != nil {
			t.Fatal(err)
		}
		_, format, decodeErr := image.Decode(file)
		_ = file.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if format != wantFormat {
			t.Fatalf("format = %q, want %q for %s", format, wantFormat, extension)
		}
	}
}

func writeSolidPNG(t *testing.T, path string, rect image.Rectangle, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func writeSolidColorPNG(t *testing.T, path string, rect image.Rectangle, c color.Color) {
	t.Helper()
	img := image.NewNRGBA(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.Set(x, y, c)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func writeGradientPNG(t *testing.T, path string, rect image.Rectangle) {
	t.Helper()
	img := image.NewRGBA(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: 20, B: 30, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

type fakeFaceDetector struct {
	face image.Rectangle
	err  error
}

func (f fakeFaceDetector) DetectFace(string, image.Image) (image.Rectangle, error) {
	return f.face, f.err
}

func syntheticCascade(highScore, lowScore float32) []byte {
	packet := make([]byte, 28)
	binary.LittleEndian.PutUint32(packet[8:12], 1)
	binary.LittleEndian.PutUint32(packet[12:16], 1)
	copy(packet[16:20], []byte{0, 128, 0, 127})
	binary.LittleEndian.PutUint32(packet[20:24], math.Float32bits(highScore))
	binary.LittleEndian.PutUint32(packet[24:28], math.Float32bits(lowScore))
	return append(packet, 0, 0, 0, 0)
}

func writeOrientedJPEG(t *testing.T, path string, orientation byte) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 40, 60))
	quadrants := []struct {
		rect image.Rectangle
		c    color.NRGBA
	}{
		{image.Rect(0, 0, 20, 30), color.NRGBA{R: 255, A: 255}},
		{image.Rect(20, 0, 40, 30), color.NRGBA{G: 255, A: 255}},
		{image.Rect(0, 30, 20, 60), color.NRGBA{B: 255, A: 255}},
		{image.Rect(20, 30, 40, 60), color.NRGBA{R: 255, G: 255, A: 255}},
	}
	for _, quadrant := range quadrants {
		for y := quadrant.rect.Min.Y; y < quadrant.rect.Max.Y; y++ {
			for x := quadrant.rect.Min.X; x < quadrant.rect.Max.X; x++ {
				img.SetNRGBA(x, y, quadrant.c)
			}
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	exif := []byte{
		0xff, 0xe1, 0x00, 0x22,
		'E', 'x', 'i', 'f', 0x00, 0x00,
		'I', 'I', 0x2a, 0x00, 0x08, 0x00, 0x00, 0x00,
		0x01, 0x00,
		0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, orientation, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	data := append([]byte{0xff, 0xd8}, exif...)
	data = append(data, encoded.Bytes()[2:]...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func colorDistance(a, b color.NRGBA) int {
	return absInt(int(a.R)-int(b.R)) + absInt(int(a.G)-int(b.G)) + absInt(int(a.B)-int(b.B))
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
