package poster

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	pigo "github.com/esimov/pigo/core"
	_ "golang.org/x/image/webp"
)

const posterRatio = 1.42

type Options struct {
	AddLabel     bool
	SubMarkPath  string
	UncMarkPath  string
	HardSub      bool
	UncensoredUC bool
	FaceDetector FaceDetector
}

type FaceDetector interface {
	DetectFace(fanartPath string, img image.Image) (image.Rectangle, error)
}

type PigoDetector struct {
	classifier *pigo.Pigo
}

func NewPigoDetector(cascadePath string) (*PigoDetector, error) {
	packet, err := os.ReadFile(cascadePath)
	if err != nil {
		return nil, fmt.Errorf("read Pigo cascade %s: %w", cascadePath, err)
	}
	if err := validateCascade(packet); err != nil {
		return nil, fmt.Errorf("parse Pigo cascade %s: %w", cascadePath, err)
	}
	classifier, err := unpackCascade(packet)
	if err != nil {
		return nil, fmt.Errorf("parse Pigo cascade %s: %w", cascadePath, err)
	}
	return &PigoDetector{classifier: classifier}, nil
}

func validateCascade(packet []byte) error {
	if len(packet) < 16 {
		return fmt.Errorf("cascade is truncated: got %d bytes", len(packet))
	}
	depth := binary.LittleEndian.Uint32(packet[8:12])
	trees := binary.LittleEndian.Uint32(packet[12:16])
	if depth == 0 || depth > 20 {
		return fmt.Errorf("invalid tree depth %d", depth)
	}
	if trees == 0 {
		return fmt.Errorf("cascade contains no trees")
	}
	leaves := uint64(1) << depth
	required := uint64(16) + uint64(trees)*8*leaves
	if required > uint64(len(packet)) {
		return fmt.Errorf("cascade is truncated: got %d bytes, need %d", len(packet), required)
	}
	return nil
}

func unpackCascade(packet []byte) (classifier *pigo.Pigo, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			classifier = nil
			err = fmt.Errorf("invalid cascade data: %v", recovered)
		}
	}()
	return pigo.NewPigo().Unpack(packet)
}

func (p *PigoDetector) DetectFace(_ string, img image.Image) (image.Rectangle, error) {
	if p == nil || p.classifier == nil {
		return image.Rectangle{}, fmt.Errorf("Pigo detector is not initialized")
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	maxSize := min(width, height)
	if maxSize <= 0 {
		return image.Rectangle{}, nil
	}
	minSize := min(20, maxSize)
	pixels := grayscale(img)
	detections := p.classifier.RunCascade(pigo.CascadeParams{
		MinSize:     minSize,
		MaxSize:     maxSize,
		ShiftFactor: 0.1,
		ScaleFactor: 1.1,
		ImageParams: pigo.ImageParams{
			Pixels: pixels,
			Rows:   height,
			Cols:   width,
			Dim:    width,
		},
	}, 0)
	clusters := p.classifier.ClusterDetections(detections, 0.2)
	if len(clusters) == 0 {
		return image.Rectangle{}, nil
	}
	best := clusters[0]
	for _, detection := range clusters[1:] {
		if detection.Q > best.Q {
			best = detection
		}
	}
	left := best.Col - best.Scale/2
	top := best.Row - best.Scale/2
	face := image.Rect(left, top, left+best.Scale, top+best.Scale)
	face = face.Intersect(image.Rect(0, 0, width, height))
	return face.Add(bounds.Min), nil
}

func grayscale(img image.Image) []uint8 {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	pixels := make([]uint8, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			gray := color.GrayModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.Gray)
			pixels[y*width+x] = gray.Y
		}
	}
	return pixels
}

func Process(fanartPath string, posterPath string, opts Options) error {
	fanart, err := loadImage(fanartPath)
	if err != nil {
		return err
	}
	out := cropDefault(fanart, posterRatio)
	if opts.FaceDetector != nil {
		if face, err := opts.FaceDetector.DetectFace(fanartPath, fanart); err == nil && !face.Empty() {
			out = cropByFace(fanart, face, posterRatio)
		}
	}
	if opts.AddLabel {
		if opts.HardSub && opts.SubMarkPath != "" {
			if err := overlayMark(out, opts.SubMarkPath, bottomRight); err != nil {
				return err
			}
		}
		if opts.UncensoredUC && opts.UncMarkPath != "" {
			if err := overlayMark(out, opts.UncMarkPath, bottomLeft); err != nil {
				return err
			}
		}
	}
	return saveImage(out, posterPath)
}

func Validate(path string) error {
	if _, err := loadImage(path); err != nil {
		return fmt.Errorf("validate image %s: %w", path, err)
	}
	return nil
}

func Normalize(sourcePath, destinationPath string) error {
	img, err := loadImage(sourcePath)
	if err != nil {
		return fmt.Errorf("normalize image %s: %w", sourcePath, err)
	}
	if err := saveImage(img, destinationPath); err != nil {
		return fmt.Errorf("normalize image to %s: %w", destinationPath, err)
	}
	return nil
}

func cropDefault(src image.Image, ratio float64) *image.RGBA {
	bounds := src.Bounds()
	fanartW := bounds.Dx()
	fanartH := bounds.Dy()
	posterW, posterH := posterSize(fanartW, fanartH, ratio)
	dh := (fanartH - posterH) / 2
	srcRect := image.Rect(bounds.Max.X-posterW, bounds.Min.Y+dh, bounds.Max.X, bounds.Min.Y+dh+posterH)
	dst := image.NewRGBA(image.Rect(0, 0, posterW, posterH))
	draw.Draw(dst, dst.Bounds(), src, srcRect.Min, draw.Src)
	return dst
}

func cropByFace(src image.Image, face image.Rectangle, ratio float64) *image.RGBA {
	bounds := src.Bounds()
	fanartW := bounds.Dx()
	fanartH := bounds.Dy()
	posterW, posterH := posterSize(fanartW, fanartH, ratio)
	centerX := float64(face.Min.X-bounds.Min.X) + float64(face.Dx())/2
	left := int(centerX - float64(posterW)/2)
	if left < 0 {
		left = 0
	}
	maxLeft := fanartW - posterW
	if left > maxLeft {
		left = maxLeft
	}
	dh := (fanartH - posterH) / 2
	srcRect := image.Rect(bounds.Min.X+left, bounds.Min.Y+dh, bounds.Min.X+left+posterW, bounds.Min.Y+dh+posterH)
	dst := image.NewRGBA(image.Rect(0, 0, posterW, posterH))
	draw.Draw(dst, dst.Bounds(), src, srcRect.Min, draw.Src)
	return dst
}

func posterSize(fanartW int, fanartH int, ratio float64) (int, int) {
	posterW := fanartW
	posterH := fanartH
	if float64(fanartH)/float64(fanartW) < ratio {
		posterW = int(float64(fanartH) / ratio)
	} else {
		posterH = int(float64(fanartW) * ratio)
	}
	return posterW, posterH
}

type markPosition int

const (
	bottomLeft markPosition = iota
	bottomRight
)

func overlayMark(dst *image.RGBA, markPath string, pos markPosition) error {
	mark, err := loadImage(markPath)
	if err != nil {
		return err
	}
	markBounds := mark.Bounds()
	x := 0
	if pos == bottomRight {
		x = dst.Bounds().Dx() - markBounds.Dx()
	}
	y := dst.Bounds().Dy() - markBounds.Dy()
	rect := image.Rect(x, y, x+markBounds.Dx(), y+markBounds.Dy())
	draw.Draw(dst, rect, mark, markBounds.Min, draw.Over)
	return nil
}

func loadImage(path string) (image.Image, error) {
	img, err := imaging.Open(path, imaging.AutoOrientation(true))
	if err != nil {
		return nil, err
	}
	return img, nil
}

func saveImage(img image.Image, path string) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".javsp-image-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	encodeErr := encodeImage(temp, img, filepath.Ext(path))
	closeErr := temp.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func encodeImage(writer io.Writer, img image.Image, extension string) error {
	switch strings.ToLower(extension) {
	case ".jpg", ".jpeg":
		return jpeg.Encode(writer, img, &jpeg.Options{Quality: 95})
	case ".png":
		return png.Encode(writer, img)
	default:
		return fmt.Errorf("unsupported output image extension %q", extension)
	}
}
